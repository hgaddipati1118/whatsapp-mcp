package main

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

func TestExtractReplyContextFromExtendedText(t *testing.T) {
	message := &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String("Nested answer"),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:    proto.String("parent-message"),
				Participant: proto.String("14155551234@s.whatsapp.net"),
				QuotedMessage: &waProto.Message{
					Conversation: proto.String("Original question"),
				},
			},
		},
	}

	reply := extractReplyContext(message)
	if reply.MessageID != "parent-message" {
		t.Fatalf("expected parent-message, got %q", reply.MessageID)
	}
	if reply.Sender != "14155551234@s.whatsapp.net" {
		t.Fatalf("unexpected sender %q", reply.Sender)
	}
	if reply.Text != "Original question" {
		t.Fatalf("unexpected quoted text %q", reply.Text)
	}
}

func TestApplyReplyContextUsesWhatsAppQuotedMessageFields(t *testing.T) {
	message := &waProto.Message{Conversation: proto.String("Native reply")}
	applyReplyContext(message, nativeReplyContext{
		MessageID: "parent-message",
		Sender:    "14155551234@s.whatsapp.net",
		Text:      "Original question",
	})

	extended := message.GetExtendedTextMessage()
	if extended == nil || extended.GetText() != "Native reply" {
		t.Fatal("plain text should be promoted to an extended text message")
	}
	contextInfo := extended.GetContextInfo()
	if contextInfo.GetStanzaID() != "parent-message" {
		t.Fatalf("unexpected stanza ID %q", contextInfo.GetStanzaID())
	}
	if contextInfo.GetParticipant() != "14155551234@s.whatsapp.net" {
		t.Fatalf("unexpected participant %q", contextInfo.GetParticipant())
	}
	if contextInfo.GetQuotedMessage().GetConversation() != "Original question" {
		t.Fatalf("unexpected quote %q", contextInfo.GetQuotedMessage().GetConversation())
	}
}

func TestEnsureMessageSchemaAddsReplyColumns(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE messages (
		id TEXT,
		chat_jid TEXT,
		sender TEXT,
		content TEXT,
		timestamp TIMESTAMP,
		is_from_me BOOLEAN,
		media_type TEXT,
		filename TEXT
	)`); err != nil {
		t.Fatal(err)
	}

	if err = ensureMessageReplySchema(db); err != nil {
		t.Fatal(err)
	}

	columns := map[string]bool{}
	rows, err := db.Query("PRAGMA table_info(messages)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue interface{}
		if err = rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	for _, name := range []string{"reply_to_message_id", "reply_to_sender", "reply_to_text"} {
		if !columns[name] {
			t.Fatalf("missing migrated column %s", name)
		}
	}
}
