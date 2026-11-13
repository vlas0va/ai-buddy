-- Из черновика какого сообщения ассистента отправлено обращение: чат показывает «Сообщение отправлено» только на этом
-- черновике и никогда не отправляет его дважды (отзыв 12.11).
ALTER TABLE core.escalations ADD COLUMN IF NOT EXISTS message_id uuid REFERENCES core.chat_messages(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX IF NOT EXISTS escalations_message_idx ON core.escalations (message_id) WHERE message_id IS NOT NULL;
