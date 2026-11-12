#!/usr/bin/env python3
"""Regenerate contracts/schemas/*.json. Usage: python3 scripts/gen_schemas.py contracts/schemas"""
import json, os, sys
out = sys.argv[1]
S = "https://json-schema.org/draft/2020-12/schema"
date = {"type": "string", "format": "date"}
idem = {"type": "string", "format": "uuid", "description": "= proposal_id; repeated call returns stored result"}
conf = {"type": "string", "description": "Issued by core on user confirmation; see contracts/mcp-tools.md"}
def obj(props, req=None):
    o = {"type": "object", "additionalProperties": False, "properties": props}
    if req: o["required"] = req
    return o
def tool(name, desc, roles, write, inp, outp):
    return {"name": name, "description": desc, "roles": roles, "write": write,
            "inputSchema": {"$schema": S, **inp}, "outputSchema": {"$schema": S, **outp}}
abs_type = {"type": "string", "enum": ["day_off", "vacation", "absence", "parental_leave"],
            "description": "day_off = отгул, vacation = отпуск, absence = отсутствие, parental_leave = декрет"}
abs_status = {"type": "string", "enum": ["pending_approval", "pending_hr", "approved", "rejected", "cancelled"],
              "description": "pending_hr = the lead approved, the HR admin has not decided yet (dates can still be moved)"}
abs_doc = obj({"name": {"type": "string", "maxLength": 200}, "mime": {"type": "string", "maxLength": 100},
               "size": {"type": "integer", "minimum": 0}}, ["name"])
abs_docs = {"type": "array", "maxItems": 3, "items": abs_doc, "description": "Metadata of attached documents (never their content)"}
abs_req = obj({
    "id": {"type": "string", "pattern": "^ABS-[0-9]{6}$"},
    "type": abs_type,
    "start_date": date, "end_date": date,
    "comment": {"type": "string"},
    "status": abs_status,
    "approver_id": {"type": "string"}, "approver_display_name": {"type": "string"},
    "created_at": {"type": "string", "format": "date-time"},
    "documents": abs_docs,
    "steps": {"type": "array", "description": "Lead approves, HR admin registers in the HR system, HR BP is notified",
              "items": obj({"role": {"type": "string", "enum": ["lead", "hr_admin", "hrbp"]},
                            "display_name": {"type": "string"},
                            "status": {"type": "string", "enum": ["waiting", "approved", "rejected", "registered", "notified", "skipped"]},
                            "at": {"type": "string", "format": "date-time"}}, ["role", "status"])},
    "replayed": {"type": "boolean"}},
    ["id", "type", "start_date", "end_date", "status", "approver_id", "created_at"])
absence = [
  tool("get_policy", "Тестовая инструкция по оформлению отгула для команды пользователя (team из JWT).", ["intern","mentor","lead"], False,
       obj({}), obj({"doc_id": {"type":"string"}, "title": {"type":"string"}, "text": {"type":"string"},
                     "source_type": {"const": "test"}, "url": {"type":"string"}}, ["doc_id","title","text","source_type"])),
  tool("list_my_requests", "Заявки на отсутствие текущего пользователя.", ["intern","mentor","lead"], False,
       obj({"status": abs_status}),
       obj({"items": {"type":"array","items": abs_req}}, ["items"])),
  tool("validate_request", "Проверка без записи по виду: отгул — рабочие дни, до 3 дней; отпуск — за 14 дней, до 28 дней; отсутствие — до 5 дней; декрет — нужен документ. Нет пересечений.", ["intern","mentor","lead"], False,
       obj({"type": abs_type, "start_date": date, "end_date": date, "documents": abs_docs,
            "exclude_id": {"type":"string","pattern":"^ABS-[0-9]{6}$","description":"the request being moved"}}, ["type","start_date","end_date"]),
       obj({"valid": {"type":"boolean"}, "problems": {"type":"array","items":{"type":"string"}},
            "approver_id": {"type":"string"}, "approver_display_name": {"type":"string"}}, ["valid","problems"])),
  tool("create_request", "Создать заявку на отсутствие (отгул, отпуск, отсутствие, декрет) от имени пользователя из JWT. Согласует руководитель, затем HR-администратор. Только после подтверждения.", ["intern","mentor","lead"], True,
       obj({"type": abs_type, "start_date": date, "end_date": date, "documents": abs_docs,
            "comment": {"type":"string","maxLength":500}, "idempotency_key": idem, "confirmation_token": conf},
           ["type","start_date","end_date","idempotency_key","confirmation_token"]),
       abs_req),
  tool("get_request", "Заявка по номеру; только своя.", ["intern","mentor","lead"], False,
       obj({"id": {"type":"string","pattern":"^ABS-[0-9]{6}$"}}, ["id"]), abs_req),
  tool("reschedule_request", "Перенести свою заявку на другие даты, пока HR-администратор не согласовал. Согласование начинается заново. Только после подтверждения.", ["intern","mentor","lead"], True,
       obj({"id": {"type":"string","pattern":"^ABS-[0-9]{6}$"}, "start_date": date, "end_date": date,
            "idempotency_key": idem, "confirmation_token": conf}, ["id","start_date","end_date","idempotency_key","confirmation_token"]),
       abs_req),
]
appr_item = obj({"id": {"type":"string"}, "kind": {"type":"string","enum":["absence_request","access_request","trip_request"]},
                 "ref_id": {"type":"string"}, "requester_id": {"type":"string"}, "requester_display_name": {"type":"string"},
                 "summary": {"type":"string"}, "status": {"type":"string","enum":["pending","approved","rejected","cancelled"]},
                 "stage": {"type":"string","enum":["lead","hr_admin"],"description":"absence: the lead first, then the HR admin"},
                 "comment": {"type":"string"}, "created_at": {"type":"string","format":"date-time"}, "replayed": {"type":"boolean"}},
                ["id","kind","ref_id","requester_id","summary","status","created_at"])
approvals = [
  tool("list_pending", "Запросы на согласование, где согласующий — пользователь из JWT.", ["lead","hr_admin"], False,
       obj({}), obj({"items": {"type":"array","items": appr_item}}, ["items"])),
  tool("decide", "Согласовать или отклонить запрос, где согласующий — пользователь из JWT (руководитель или HR-администратор).", ["lead","hr_admin"], True,
       obj({"approval_id": {"type":"string"}, "decision": {"type":"string","enum":["approved","rejected"]},
            "comment": {"type":"string","maxLength":1000}, "idempotency_key": idem, "confirmation_token": conf},
           ["approval_id","decision","idempotency_key","confirmation_token"]),
       appr_item),
]
contact = obj({"role": {"type":"string","enum":["mentor","lead","hrbp","curator"]}, "employee_id": {"type":"string"},
               "display_name": {"type":"string"}, "channel": {"type":"string","description":"Название канала или чата, без личных контактов"},
               "ask_about": {"type":"array","items":{"type":"string"}}}, ["role","employee_id","display_name"])
directory = [
  tool("whoami", "Профиль пользователя из JWT: команда, роль, начало стажировки, текущий период.", ["intern","mentor","lead","hrbp","alumni","hr_admin"], False,
       obj({}), obj({"employee_id":{"type":"string"},"display_name":{"type":"string"},"role":{"type":"string"},
                     "team_id":{"type":"string"},"team_name":{"type":"string"},"cohort_start":date,
                     "current_period":{"type":"string","enum":["week_1","week_2","month_1","month_2","after_3_months"]}},
                    ["employee_id","display_name","role","team_id"])),
  tool("get_my_contacts", "Свои наставник, руководитель, HR BP и куратор стажировки и с чем к ним идти.", ["intern","mentor","lead","hrbp","alumni","hr_admin"], False,
       obj({}), obj({"items":{"type":"array","items":contact}}, ["items"])),
]
cl_item = obj({"id":{"type":"string","pattern":"^CL-[0-9]{2}$"}, "role":{"type":"string","enum":["intern","mentor","both"]},
               "period":{"type":"string","enum":["week_1","week_2","month_1","month_2","after_3_months"]},
               "title":{"type":"string"}, "description":{"type":"string"},
               "steps":{"type":"array","items":{"type":"string"}},
               "scenarios":{"type":"array","items":{"type":"string","enum":["S1","S2","S3","S4"]}},
               "human_only":{"type":"boolean"}}, ["id","role","period","title","scenarios","human_only"])
checklist = [
  tool("get_plan_template", "Пункты чек-листа Авито для роли (data/checklist/checklist.yaml) с учётом команды.", ["intern","mentor","lead","hrbp","alumni","hr_admin"], False,
       obj({"role": {"type":"string","enum":["intern","mentor"]}}, ["role"]),
       obj({"periods":{"type":"array","items":obj({"id":{"type":"string"},"label":{"type":"string"}},["id","label"])},
            "items":{"type":"array","items":cl_item}}, ["periods","items"])),
]
acc_env = {"type":"string","enum":["dev","stage","prod"]}
acc_level = {"type":"string","enum":["read","write","admin"],"description":"чтение, запись, администрирование"}
res = obj({"resource_id":{"type":"string"},"name":{"type":"string"},
           "kind":{"type":"string","enum":["tool","space","channel","project","repo","database"]},
           "elevated":{"type":"boolean"},"status":{"type":"string","enum":["not_requested","pending_approval","granted","rejected"]},
           "checklist_item_id":{"type":"string"},
           "environments":{"type":"array","items":acc_env,"description":"empty = the resource has no environments"},
           "levels":{"type":"array","items":acc_level},
           "optional":{"type":"boolean","description":"requested on demand, not part of the onboarding checklist"},
           "approver_label":{"type":"string","description":"who approves write/admin/prod rights"},
           "environment":acc_env, "level":acc_level}, ["resource_id","name","kind","elevated","status"])
acc_req = obj({"id":{"type":"string","pattern":"^ACC-[0-9]{6}$"},"resource_id":{"type":"string"},"resource_name":{"type":"string"},
               "status":{"type":"string","enum":["pending_approval","granted","rejected"]},"approver_id":{"type":["string","null"]},
               "checklist_item_id":{"type":["string","null"],"description":"Plan task to link the action to (R66)"},
               "environment":{"type":["string","null"],"enum":["dev","stage","prod",None]},"level":acc_level,
               "created_at":{"type":"string","format":"date-time"},"replayed":{"type":"boolean"}},
              ["id","resource_id","status","created_at"])
access = [
  tool("list_required", "Доступы, нужные команде пользователя (Confluence, Jira, пространство кластера/команды), со статусом для него.", ["intern","mentor"], False,
       obj({}), obj({"items":{"type":"array","items":res}}, ["items"])),
  tool("list_my_access", "Выданные и запрошенные доступы пользователя.", ["intern","mentor"], False,
       obj({}), obj({"items":{"type":"array","items":res}}, ["items"])),
  tool("list_team_channels", "Каналы и чаты команды (только названия и назначение, без участников).", ["intern","mentor"], False,
       obj({}), obj({"items":{"type":"array","items":obj({"name":{"type":"string"},"purpose":{"type":"string"},"closed":{"type":"boolean"}},["name","closed"])}}, ["items"])),
  tool("request_access", "Заявка на доступ для пользователя из JWT: конкретный ресурс (проект Jira, репозиторий, БД), окружение и уровень прав. Запись, администрирование и prod уходят на согласование руководителю.", ["intern","mentor"], True,
       obj({"resource_id":{"type":"string"},"reason":{"type":"string","maxLength":500},
            "environment":acc_env,"level":{**acc_level,"default":"read"},"idempotency_key":idem,"confirmation_token":conf},
           ["resource_id","idempotency_key","confirmation_token"]),
       acc_req),
]
kb = [
  tool("search", "Поиск по базе знаний среди документов, доступных роли и команде пользователя (ACL до LLM). Все документы — тестовые.", ["intern","mentor","lead","hrbp","alumni","hr_admin"], False,
       obj({"query":{"type":"string","minLength":2,"maxLength":500},"k":{"type":"integer","minimum":1,"maximum":10,"default":5}}, ["query"]),
       obj({"items":{"type":"array","items":obj({"doc_id":{"type":"string"},"title":{"type":"string"},"snippet":{"type":"string"},
            "source_type":{"const":"test"},"url":{"type":"string"},"score":{"type":"number"}},["doc_id","title","snippet","source_type"])}}, ["items"])),
  tool("add_faq", "Добавить ответ наставника в базу знаний его команды (сценарий S2, флаг escalation_to_kb). Документ виден ролям intern, mentor, lead этой команды.", ["mentor"], True,
       obj({"title":{"type":"string","minLength":3,"maxLength":300},"body":{"type":"string","minLength":3,"maxLength":8000},
            "idempotency_key":idem,"confirmation_token":conf}, ["title","body","idempotency_key","confirmation_token"]),
       obj({"doc_id":{"type":"string"},"title":{"type":"string"},"source_type":{"const":"test"},"replayed":{"type":"boolean"}}, ["doc_id","title","source_type"])),
]
trip_req = obj({"id":{"type":"string","pattern":"^TRP-[0-9]{6}$"},"city":{"type":"string"},"start_date":date,"end_date":date,
                "purpose":{"type":"string"},"status":{"type":"string","enum":["pending_approval","approved","rejected","cancelled"]},
                "approver_id":{"type":"string"},"approver_display_name":{"type":"string"},
                "created_at":{"type":"string","format":"date-time"},"replayed":{"type":"boolean"}},
               ["id","city","start_date","end_date","purpose","status","approver_id","created_at"])
trip_in = {"city":{"type":"string","minLength":2,"maxLength":80},"start_date":date,"end_date":date,
           "purpose":{"type":"string","minLength":3,"maxLength":300}}
trips = [
  tool("get_policy", "Правила командировок (тестовые): офисы, срок подачи, длительность.", ["intern","mentor","lead"], False,
       obj({}), obj({"offices":{"type":"array","items":{"type":"string"}},"min_notice_days":{"type":"integer"},
                     "max_days":{"type":"integer"},"text":{"type":"string"}}, ["offices","min_notice_days","max_days","text"])),
  tool("list_my_requests", "Заявки на командировку текущего пользователя.", ["intern","mentor","lead"], False,
       obj({}), obj({"items":{"type":"array","items":trip_req}}, ["items"])),
  tool("validate_request", "Проверка без записи: офис из списка, не свой город, срок подачи, длительность, нет пересечений.", ["intern","mentor","lead"], False,
       obj(trip_in, ["city","start_date","end_date","purpose"]),
       obj({"valid":{"type":"boolean"},"problems":{"type":"array","items":{"type":"string"}},
            "approver_id":{"type":"string"},"approver_display_name":{"type":"string"}}, ["valid","problems"])),
  tool("create_request", "Создать заявку на командировку от имени пользователя из JWT; согласует руководитель. Только после подтверждения.", ["intern","mentor","lead"], True,
       obj({**trip_in, "idempotency_key": idem, "confirmation_token": conf}, ["city","start_date","end_date","purpose","idempotency_key","confirmation_token"]),
       trip_req),
]
consent = obj({"consent_id":{"type":"string"},"title":{"type":"string"},"description":{"type":"string"},
               "signable":{"type":"boolean"},"required":{"type":"boolean"},
               "status":{"type":"string","enum":["signed","not_signed"]},"signed_via":{"type":"string","enum":["hr_portal","buddy"]},
               "signed_at":{"type":"string","format":"date-time"}}, ["consent_id","title","signable","required","status"])
signature = obj({"id":{"type":"string","pattern":"^CNS-[0-9]{6}$"},"consent_id":{"type":"string"},"title":{"type":"string"},
                 "status":{"const":"signed"},"signed_at":{"type":"string","format":"date-time"},"replayed":{"type":"boolean"}},
                ["id","consent_id","title","status","signed_at"])
consents = [
  tool("list", "Согласия сотрудника со статусами: подписанные в кадровом портале при оформлении и добровольные.", ["intern","mentor","lead","hrbp","alumni","hr_admin"], False,
       obj({}), obj({"items":{"type":"array","items":consent}}, ["items"])),
  tool("sign", "Подписать добровольное согласие от имени пользователя из JWT. Только после подтверждения.", ["intern","mentor","lead","hrbp","alumni","hr_admin"], True,
       obj({"consent_id":{"type":"string"},"idempotency_key":idem,"confirmation_token":conf}, ["consent_id","idempotency_key","confirmation_token"]),
       signature),
]
for name, tools in [("absence",absence),("approvals",approvals),("directory",directory),("checklist",checklist),("access",access),("kb",kb),("trips",trips),("consents",consents)]:
    with open(os.path.join(out, f"{name}.json"), "w", encoding="utf-8") as f:
        json.dump({"server": name, "tools": tools}, f, ensure_ascii=False, indent=2); f.write("\n")
print("ok")
