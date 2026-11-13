package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"aibuddy/services/internal/core/actions"
	"aibuddy/services/internal/core/agentclient"
	"aibuddy/services/internal/platform/apierr"
)

// Ограничения вложений (contracts/openapi.yaml MessageAttachment). Браузер читает текстовые файлы (логи, конфиги, код)
// и отправляет их текст; для скриншотов и документов — только метаданные, поэтому их содержимое не попадает ни в ядро,
// ни к агенту, ни в LLM.
const (
	maxAttachments      = 3
	maxAttachmentText   = 32 << 10 // байт текста одного текстового вложения
	maxAttachmentName   = 200      // рун
	maxAttachmentSize   = 20 << 20 // заявленный размер файла, байт
	maxMessageBodyBytes = 256 << 10
)

var attachmentKinds = map[string]bool{"text": true, "image": true, "document": true}

// validateAttachments проверяет и нормализует вложения сообщения пользователя. Текст сохраняется только для вида text.
func validateAttachments(in []agentclient.Attachment) ([]agentclient.Attachment, error) {
	if len(in) > maxAttachments {
		return nil, apierr.Validation().WithMessage(fmt.Sprintf("Можно прикрепить не больше %d файлов", maxAttachments))
	}
	out := make([]agentclient.Attachment, 0, len(in))
	for _, a := range in {
		a.Name = strings.TrimSpace(a.Name)
		if a.Name == "" || utf8.RuneCountInString(a.Name) > maxAttachmentName {
			return nil, apierr.Validation().WithMessage("Имя файла: от 1 до 200 символов")
		}
		if !attachmentKinds[a.Kind] {
			return nil, apierr.Validation().WithMessage("Тип вложения: text, image или document")
		}
		if a.Size < 0 || a.Size > maxAttachmentSize {
			return nil, apierr.Validation().WithMessage("Файл больше 20 МБ")
		}
		if len(a.Mime) > 100 {
			a.Mime = a.Mime[:100]
		}
		if a.Kind != "text" {
			a.Text = ""
		} else if len(a.Text) > maxAttachmentText {
			return nil, apierr.Validation().WithMessage("Текст файла: до 32 КБ, пришлите нужный фрагмент")
		}
		out = append(out, a)
	}
	return out, nil
}

// saveAttachments сохраняет вложения сообщения пользователя.
func saveAttachments(ctx context.Context, q actions.DBTX, messageID string, atts []agentclient.Attachment) error {
	if len(atts) == 0 {
		return nil
	}
	raw, err := json.Marshal(atts)
	if err != nil {
		return fmt.Errorf("encode attachments: %w", err)
	}
	if _, err := q.Exec(ctx, `UPDATE core.chat_messages SET attachments = $2 WHERE id = $1`, messageID, raw); err != nil {
		return fmt.Errorf("save attachments: %w", err)
	}
	return nil
}
