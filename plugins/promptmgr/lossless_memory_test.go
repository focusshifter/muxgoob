package promptmgr

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/database"
	chatmemory "github.com/focusshifter/muxgoob/internal/memory"
	"github.com/focusshifter/muxgoob/utils/facts"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestPersonFactsPreserveAllDistinctFactsOnSaveAndRead(t *testing.T) {
	for _, cutover := range []bool{false, true} {
		t.Run(fmt.Sprintf("cutover=%v", cutover), func(t *testing.T) {
			db := testutils.SetupTestDB(t)
			previousDB := database.DB
			database.DB = db
			t.Cleanup(func() { database.DB = previousDB; db.Close() })
			EnsureTables()
			if err := chatmemory.EnsureSchema(db); err != nil {
				t.Fatal(err)
			}
			if cutover {
				if _, err := db.Exec(`INSERT INTO memory_migration_scopes(chat_id,state,updated_at) VALUES (123,'cutover',1)`); err != nil {
					t.Fatal(err)
				}
			}
			dossier := &facts.Dossier{}
			for i := 0; i < 50; i++ {
				dossier.Identity = append(dossier.Identity, fmt.Sprintf("Independent personal detail with marker %03d", i))
			}
			if err := SavePersonFacts(123, 42, facts.RenderDossier(dossier)); err != nil {
				t.Fatal(err)
			}
			assertAll := func(label, text string) {
				t.Helper()
				if got := len(facts.ParseDossier(text).Identity); got != 50 {
					t.Fatalf("%s lost facts: %d", label, got)
				}
				for _, body := range dossier.Identity {
					if !strings.Contains(text, body) {
						t.Fatalf("%s missing %q", label, body)
					}
				}
			}
			text, err := GetPersonFacts(123, 42)
			if err != nil {
				t.Fatal(err)
			}
			assertAll("single read", text)
			many, err := GetPersonFactsMulti(123, []int64{42})
			if err != nil {
				t.Fatal(err)
			}
			assertAll("multi read", many[42])
			if cutover {
				id := int64(42)
				entries, err := chatmemory.NewRepository(db).List(context.Background(), chatmemory.Filter{ChatID: 123, Kind: chatmemory.PersonFact, SubjectUserID: &id})
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 50 {
					t.Fatalf("durable memory lost facts: %d", len(entries))
				}
			}
		})
	}
}
