package types

// QARequest consolidates all parameters for KnowledgeQA and AgentQA service calls,
// replacing the previous 14-parameter method signatures.
// EventBus is passed separately to avoid circular dependency with the event package.
type QARequest struct {
	Session            *Session           // The conversation session
	Query              string             // User query text
	AssistantMessageID string             // Pre-created assistant message ID
	SummaryModelID     string             // Optional model override; empty = use agent/KB default
	CustomAgent        *CustomAgent       // Optional custom agent for config override
	KnowledgeBaseIDs   []string           // Knowledge base IDs to search (from request + @mentions)
	KnowledgeIDs       []string           // Specific knowledge (file) IDs to search
	TagScopes          []TagScope         // Tag-constrained KB scopes from @mentions
	MCPServiceIDs      []string           // Per-request MCP service IDs from @mentions
	SkillNames         []string           // Per-request preloaded skill names from @mentions
	ImageURLs          []string           // Image URLs for multimodal input
	ImageDescription   string             // VLM-generated image description (fallback for non-vision models)
	UserMessageID      string             // Created user message ID
	WebSearchEnabled   bool               // Whether web search is enabled for this request
	EnableMemory       bool               // Whether memory feature is enabled
	QuotedContext      string             // Quoted message content from IM quote-reply (appended at LLM prompt stage, not used for retrieval)
	SystemPrompt       string             // User-supplied system prompt appended to the base system prompt (stateless chat spec section 2.2)
	Attachments        MessageAttachments // File attachments (processed and ready for prompt injection)
	History            []HistoryMessage    // Conversation history for query rewrite (stateless chat)
}
