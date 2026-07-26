package main

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func newMutationTestStore(t *testing.T) *MessageStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if _, err = db.Exec(`CREATE TABLE messages (
		id TEXT,
		chat_jid TEXT,
		sender TEXT,
		content TEXT,
		timestamp TIMESTAMP,
		is_from_me BOOLEAN,
		media_type TEXT,
		filename TEXT,
		reply_to_message_id TEXT,
		reply_to_sender TEXT,
		reply_to_text TEXT,
		PRIMARY KEY (id, chat_jid)
	)`); err != nil {
		db.Close()
		t.Fatalf("create messages table: %v", err)
	}
	if err = ensureMessageReplySchema(db); err != nil {
		db.Close()
		t.Fatalf("migrate messages table: %v", err)
	}
	store := &MessageStore{db: db}
	t.Cleanup(func() {
		store.Close()
	})
	return store
}

func insertMutationTarget(t *testing.T, store *MessageStore, id, chatJID string, isFromMe bool) {
	t.Helper()
	if _, err := store.db.Exec(
		`INSERT INTO messages
		 (id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename)
		 VALUES (?, ?, ?, ?, ?, ?, '', '')`,
		id,
		chatJID,
		"15550101010@s.whatsapp.net",
		"Synthetic message",
		time.Now().UTC().Add(-time.Minute),
		isFromMe,
	); err != nil {
		t.Fatalf("insert mutation target: %v", err)
	}
}

func TestMessageSchemaAddsMutationColumns(t *testing.T) {
	store := newMutationTestStore(t)
	rows, err := store.db.Query("PRAGMA table_info(messages)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue interface{}
		if err = rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	for _, column := range []string{"reactions_json", "edited_at"} {
		if !columns[column] {
			t.Fatalf("expected migrated column %q", column)
		}
	}
}

func TestStoredReactionsReplaceAndRemoveByActor(t *testing.T) {
	store := newMutationTestStore(t)
	insertMutationTarget(t, store, "msg-target", "15550101010@s.whatsapp.net", false)

	if err := store.SetMessageReaction(
		"15550101010@s.whatsapp.net",
		"msg-target",
		"participant-a",
		"👍",
		false,
	); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMessageReaction(
		"15550101010@s.whatsapp.net",
		"msg-target",
		"me",
		"❤️",
		true,
	); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMessageReaction(
		"15550101010@s.whatsapp.net",
		"msg-target",
		"participant-a",
		"😂",
		false,
	); err != nil {
		t.Fatal(err)
	}

	var encoded string
	if err := store.db.QueryRow(
		"SELECT reactions_json FROM messages WHERE chat_jid = ? AND id = ?",
		"15550101010@s.whatsapp.net",
		"msg-target",
	).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	var reactions []storedReaction
	if err := json.Unmarshal([]byte(encoded), &reactions); err != nil {
		t.Fatal(err)
	}
	byActor := map[string]storedReaction{}
	for _, reaction := range reactions {
		byActor[reaction.Actor] = reaction
	}
	if len(reactions) != 2 || byActor["participant-a"].Emoji != "😂" || byActor["me"].Emoji != "❤️" {
		t.Fatalf("unexpected reactions: %+v", reactions)
	}

	if err := store.SetMessageReaction(
		"15550101010@s.whatsapp.net",
		"msg-target",
		"me",
		"",
		true,
	); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(
		"SELECT reactions_json FROM messages WHERE chat_jid = ? AND id = ?",
		"15550101010@s.whatsapp.net",
		"msg-target",
	).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(encoded), &reactions); err != nil {
		t.Fatal(err)
	}
	if len(reactions) != 1 || reactions[0].Actor != "participant-a" {
		t.Fatalf("unexpected reactions after removal: %+v", reactions)
	}
}

func TestIncomingReactionEditAndRevokeUpdateStoredTarget(t *testing.T) {
	store := newMutationTestStore(t)
	chat, _ := types.ParseJID("15550101010@s.whatsapp.net")
	sender, _ := types.ParseJID("15550101010@s.whatsapp.net")
	insertMutationTarget(t, store, "msg-target", chat.String(), true)

	reaction := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     chat,
				Sender:   sender,
				IsFromMe: false,
			},
		},
		Message: &waProto.Message{
			ReactionMessage: &waProto.ReactionMessage{
				Key: &waProto.MessageKey{
					ID:        proto.String("msg-target"),
					RemoteJID: proto.String(chat.String()),
				},
				Text: proto.String("👍"),
			},
		},
	}
	if !handleStoredMessageMutation(store, reaction) {
		t.Fatal("expected reaction event to be handled")
	}

	edit := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     chat,
				Sender:   sender,
				IsFromMe: true,
			},
			ID: "msg-target",
		},
		Message: &waProto.Message{Conversation: proto.String("Edited synthetic message")},
		IsEdit:  true,
	}
	if !handleStoredMessageMutation(store, edit) {
		t.Fatal("expected edit event to be handled")
	}

	var content, reactionsJSON, editedAt string
	if err := store.db.QueryRow(
		"SELECT content, reactions_json, edited_at FROM messages WHERE chat_jid = ? AND id = ?",
		chat.String(),
		"msg-target",
	).Scan(&content, &reactionsJSON, &editedAt); err != nil {
		t.Fatal(err)
	}
	if content != "Edited synthetic message" || reactionsJSON == "" || editedAt == "" {
		t.Fatalf("unexpected edited row: content=%q reactions=%q edited_at=%q", content, reactionsJSON, editedAt)
	}

	revoke := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     chat,
				Sender:   sender,
				IsFromMe: true,
			},
		},
		Message: &waProto.Message{
			ProtocolMessage: &waProto.ProtocolMessage{
				Type: waProto.ProtocolMessage_REVOKE.Enum(),
				Key: &waProto.MessageKey{
					ID:        proto.String("msg-target"),
					RemoteJID: proto.String(chat.String()),
				},
			},
		},
	}
	if !handleStoredMessageMutation(store, revoke) {
		t.Fatal("expected revoke event to be handled")
	}
	var count int
	if err := store.db.QueryRow(
		"SELECT COUNT(*) FROM messages WHERE chat_jid = ? AND id = ?",
		chat.String(),
		"msg-target",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected revoked target to be removed, found %d rows", count)
	}
}

func TestMutationTargetRequiresExactChatAndOwnership(t *testing.T) {
	store := newMutationTestStore(t)
	insertMutationTarget(t, store, "msg-target", "15550101010@s.whatsapp.net", false)

	if _, err := store.GetMessageMutationTarget(
		"different@s.whatsapp.net",
		"msg-target",
		false,
	); err == nil {
		t.Fatal("expected exact chat mismatch to fail")
	}
	if _, err := store.GetMessageMutationTarget(
		"15550101010@s.whatsapp.net",
		"msg-target",
		true,
	); err == nil {
		t.Fatal("expected ownership requirement to fail")
	}
}
