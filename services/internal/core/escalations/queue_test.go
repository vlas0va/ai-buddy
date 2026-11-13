package escalations_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"aibuddy/services/internal/core/escalations"
	"aibuddy/services/internal/platform/authn"
)

// ask сохраняет ход чата пользователя u и кладёт его вопрос в список, как это делает чат ядра после ответа без
// попадания в базу знаний.
func (e *env) ask(u user, n int, question string) (bool, error) {
	e.t.Helper()
	sid := fmt.Sprintf("00000000-0000-0000-0000-%012d", 100+n)
	mid := fmt.Sprintf("00000000-0000-0000-0001-%012d", 100+n)
	e.exec(`INSERT INTO core.chat_sessions (id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, sid, u.id)
	e.exec(`INSERT INTO core.chat_messages (id, session_id, author, text) VALUES ($1, $2, 'assistant', 'нет ответа') ON CONFLICT DO NOTHING`, mid, sid)
	return escalations.Enqueue(context.Background(), e.pool, authn.Claims{Sub: u.id, Role: u.role, TeamID: u.team}, "mentor", question, sid, mid)
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) queue(u user) (int, []escalations.QueueGroup) {
	e.t.Helper()
	code, b := e.do(u, "GET", "/api/escalations/queue", "")
	var out []escalations.QueueGroup
	_ = json.Unmarshal(b, &out)
	return code, out
}

func TestQueue_GroupsSimilarQuestions_Organizers1311(t *testing.T) {
	e := setup(t, true)
	for i, q := range []string{
		"Можно ли приходить в офис с собакой?",
		"Где посмотреть расписание командного митапа по архитектуре?",
		"можно приходить в офис с собакой породы шпиц",
	} {
		if ok, err := e.ask(anna, i, q); !ok || err != nil {
			t.Fatalf("enqueue %d: %v %v", i, ok, err)
		}
	}
	// одно и то же сообщение ассистента попадает в список один раз; ручное обращение с приветствием присоединяется к
	// группе
	if ok, _ := e.ask(anna, 0, "Можно ли приходить в офис с собакой?"); ok {
		t.Fatal("the same message must be queued once")
	}
	e.send(anna, "mentor", "Олег, привет! Подскажи, пожалуйста: можно ли в офис с собакой приходить? В базе знаний команды ответа не нашёл.")
	if n := e.count(`SELECT count(*) FROM core.notifications WHERE user_id = 'mentor.oleg' AND dedupe_key LIKE 'escalation:%'`); n != 1 {
		t.Fatalf("queued questions notify nobody one by one, got %d notifications", n) // только ручное
	}

	code, groups := e.queue(oleg)
	if code != 200 || len(groups) != 2 {
		t.Fatalf("groups: %d %+v", code, groups)
	}
	dog := groups[0]
	if dog.Count != 3 || len(dog.IDs) != 3 || len(dog.Askers) != 1 || dog.Askers[0] != "Анна Котова" || !strings.Contains(dog.Question, "собак") {
		t.Fatalf("dog group: %+v", dog)
	}
	if strings.Contains(dog.Question, "Олег") || strings.Contains(groups[1].Question, "привет") {
		t.Fatalf("questions are shown without greetings: %+v", groups)
	}

	// один ответ закрывает всю группу, уведомляет спросившего по каждому вопросу и один раз уходит в базу знаний
	body := fmt.Sprintf(`{"text":"Нет, только собаки-поводыри.","add_to_kb":true,"also_ids":["%s","%s","not-a-uuid"]}`, dog.IDs[1], dog.IDs[2])
	if code, b := e.do(oleg, "POST", "/api/escalations/"+dog.IDs[0]+"/answer", body); code != 200 {
		t.Fatalf("answer: %d %s", code, b)
	}
	if n := e.count(`SELECT count(*) FROM core.escalations WHERE status = 'answered' AND added_to_kb`); n != 3 {
		t.Fatalf("answered in KB: %d", n)
	}
	if len(e.faq.calls) != 1 {
		t.Fatalf("one FAQ for the group, got %d", len(e.faq.calls))
	}
	if n := e.count(`SELECT count(*) FROM core.notifications WHERE user_id = 'intern.anna' AND kind = 'escalation_answer'`); n != 3 {
		t.Fatalf("answer notifications: %d", n)
	}
	if _, groups = e.queue(oleg); len(groups) != 1 || groups[0].Count != 1 {
		t.Fatalf("left in the stack: %+v", groups)
	}
}

func TestQueue_Rights_R71(t *testing.T) {
	e := setup(t, false)
	if ok, err := e.ask(anna, 1, "Где посмотреть расписание митапа?"); !ok || err != nil {
		t.Fatalf("enqueue: %v %v", ok, err)
	}
	ivanQ, _ := e.ask(ivan, 2, "Где посмотреть расписание митапа?")
	if !ivanQ {
		t.Fatal("ivan's question goes to his mentor")
	}
	// другой наставник, сам спросивший и HR BP не видят список Олега
	for _, u := range []user{sergey, anna, elena} {
		_, groups := e.queue(u)
		for _, g := range groups {
			for _, a := range g.Askers {
				if a == "Анна Котова" {
					t.Fatalf("%s sees Anna's question", u.id)
				}
			}
		}
	}
	if _, groups := e.queue(sergey); len(groups) != 1 || groups[0].Askers[0] != "Иван Белов" {
		t.Fatalf("sergey: %+v", groups)
	}
	// чужой вопрос в also_ids пропускается
	_, mine := e.queue(oleg)
	_, theirs := e.queue(sergey)
	body := fmt.Sprintf(`{"text":"В календаре команды.","also_ids":["%s"]}`, theirs[0].IDs[0])
	if code, _ := e.do(oleg, "POST", "/api/escalations/"+mine[0].IDs[0]+"/answer", body); code != 200 {
		t.Fatal("answer own")
	}
	if _, theirs = e.queue(sergey); len(theirs) != 1 {
		t.Fatal("ivan's question must stay open")
	}
	if code, _ := e.do(user{}, "GET", "/api/escalations/queue", ""); code != 401 {
		t.Fatal("anonymous")
	}
	// в список попадают только вопросы стажёров к наставнику, руководителю или HR BP; некорректные id игнорируются
	ctx := context.Background()
	for _, c := range []struct {
		claims authn.Claims
		role   string
		sid    string
	}{
		{authn.Claims{Sub: "mentor.oleg", Role: "mentor", TeamID: "delivery"}, "mentor", "00000000-0000-0000-0000-000000000101"},
		{authn.Claims{Sub: "intern.anna", Role: "intern", TeamID: "delivery"}, "curator", "00000000-0000-0000-0000-000000000101"},
		{authn.Claims{Sub: "intern.anna", Role: "intern", TeamID: "delivery"}, "mentor", "bad"},
		{authn.Claims{Sub: "intern.anna", Role: "intern", TeamID: "delivery"}, "mentor", "00000000-0000-0000-0000-000000000102"}, // сессия Ивана
	} {
		if ok, err := escalations.Enqueue(ctx, e.pool, c.claims, c.role, "вопрос", c.sid, "00000000-0000-0000-0001-000000000102"); ok || err != nil {
			t.Fatalf("%+v: %v %v", c, ok, err)
		}
	}
}
