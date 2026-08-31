package memory

import (
	"context"
	"fmt"
	"strings"
)

const maxContextEntriesPerKind = 20

// BuildContext renders active structured memory for model context. Person facts
// are included only for explicitly scoped users, while possible plans remain
// tool-retrieved so speculative ideas cannot be mistaken for commitments.
func (r *Repository) BuildContext(ctx context.Context, chatID int64, subjectUserIDs []int64) (string, error) {
	sections := []struct {
		kind  Kind
		title string
	}{
		{ChatLore, "Chat lore"},
		// Possible plans are intentionally tool-retrieved, not injected into every
		// ordinary reply where they could be mistaken for a schedule.
		{Decision, "Decisions and commitments"},
	}
	var blocks []string
	for _, section := range sections {
		entries, err := r.List(ctx, Filter{ChatID: chatID, Kind: section.kind})
		if err != nil {
			return "", err
		}
		if len(entries) > maxContextEntriesPerKind {
			entries = entries[:maxContextEntriesPerKind]
		}
		if len(entries) == 0 {
			continue
		}
		blocks = append(blocks, renderSection(section.title, entries))
	}

	seen := map[int64]struct{}{}
	var personEntries []Entry
	for _, userID := range subjectUserIDs {
		if userID == 0 {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		entries, err := r.List(ctx, Filter{ChatID: chatID, Kind: PersonFact, SubjectUserID: &userID})
		if err != nil {
			return "", err
		}
		entries = prioritizePinnedPersonFacts(entries)
		if len(entries) > maxContextEntriesPerKind {
			entries = entries[:maxContextEntriesPerKind]
		}
		personEntries = append(personEntries, entries...)
	}
	if len(personEntries) > 0 {
		blocks = append(blocks, renderSection("Scoped person facts", personEntries))
	}
	if len(blocks) == 0 {
		return "", nil
	}
	return "Structured memory (durable data; keep types distinct):\n" + strings.Join(blocks, "\n"), nil
}

func prioritizePinnedPersonFacts(entries []Entry) []Entry {
	if len(entries) < 2 {
		return entries
	}
	pinned := make([]Entry, 0, len(entries))
	replaceable := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Retention == Pinned {
			pinned = append(pinned, entry)
		} else {
			replaceable = append(replaceable, entry)
		}
	}
	return append(pinned, replaceable...)
}

func renderSection(title string, entries []Entry) string {
	lines := []string{title + ":"}
	for _, entry := range entries {
		prefix := ""
		if entry.SubjectUserID != nil {
			prefix = fmt.Sprintf("user_id=%d: ", *entry.SubjectUserID)
		}
		lines = append(lines, fmt.Sprintf("- [memory_id=%d] %s%s", entry.ID, prefix, entry.Body))
	}
	return strings.Join(lines, "\n")
}
