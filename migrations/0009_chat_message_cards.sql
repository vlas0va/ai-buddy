-- Cards of an assistant message (the draft of a request, the draft of a message to the mentor, a refusal, the next
-- step) are kept with the message, so leaving the chat and coming back shows them again (feedback 11.11).
ALTER TABLE core.chat_messages ADD COLUMN IF NOT EXISTS cards jsonb NOT NULL DEFAULT '{}';
