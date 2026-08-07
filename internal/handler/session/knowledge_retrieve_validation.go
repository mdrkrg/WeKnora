package session

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// MaxKnowledgeRetrieveHistory is the maximum number of history messages
// allowed (spec section 2.4).
const MaxKnowledgeRetrieveHistory = 100

// ValidateKnowledgeRetrieveRequest validates the structural portion of the
// retrieve wire request: query, history, mention shape, and scope existence.
// Resource ownership is checked while building search targets (service layer);
// tag-to-KB resolution is done by buildRetrieveTagScopes.
func ValidateKnowledgeRetrieveRequest(r KnowledgeRetrieveWireRequest) error {
	if strings.TrimSpace(r.Query) == "" {
		return fmt.Errorf("query cannot be empty")
	}
	if len(r.History) > MaxKnowledgeRetrieveHistory {
		return fmt.Errorf("history exceeds maximum of %d messages", MaxKnowledgeRetrieveHistory)
	}
	for i, msg := range r.History {
		if msg.Role != "user" && msg.Role != "assistant" {
			return fmt.Errorf("history[%d].role must be user or assistant", i)
		}
	}
	hasScope := strings.TrimSpace(r.KnowledgeBaseID) != "" ||
		len(nonEmpty(r.KnowledgeBaseIDs)) > 0 ||
		len(nonEmpty(r.KnowledgeIDs)) > 0
	seenMention := make(map[string]string)
	for i, item := range r.MentionedItems {
		if item.Type != "tag" || strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.KBID) == "" {
			return fmt.Errorf("mentioned_items[%d] must be a tag with non-empty id and kb_id", i)
		}
		if previous, ok := seenMention[item.ID]; ok && previous != item.KBID {
			return fmt.Errorf("tag %q is associated with multiple knowledge bases", item.ID)
		}
		seenMention[item.ID] = item.KBID
		hasScope = true
	}
	if !hasScope {
		return fmt.Errorf("at least one knowledge base, knowledge, or scoped tag is required")
	}
	return nil
}

// buildRetrieveTagScopes resolves the effective KB-local tag scopes from
// mentioned_items (scoped tags) plus bare tag_ids, mirroring the
// /knowledge-search handler. It applies the same rules:
//   - mentioned_items contribute scoped tags bound to their KB
//   - bare tag_ids are only allowed with exactly one KB in scope
//     (otherwise 400), and are merged into that KB's scope
func buildRetrieveTagScopes(r KnowledgeRetrieveWireRequest, kbIDs []string) ([]types.TagScope, error) {
	mentionScopes := tagScopesFromMentionedItems(r.MentionedItems)
	requestTagIDs := dedupRequestStrings(r.TagIDs)
	if err := validateUnscopedTagIDs(orphanTagIDsForScope(requestTagIDs, mentionScopes), kbIDs); err != nil {
		return nil, err
	}
	return mergeTagScopesFromRequestIDs(mentionScopes, requestTagIDs, kbIDs), nil
}

func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}
