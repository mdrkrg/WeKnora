// Package session Stateless chat endpoint implementation.
// Reference: docs/stateless-chat-spec.md
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MaxStatelessRequestBody is the maximum allowed request body size for the
// stateless chat endpoint (10 MB per spec section 5.1).
const MaxStatelessRequestBody = 10 * 1024 * 1024

// MaxStatelessHistoryMessages is the maximum number of history messages
// (100 = 50 rounds per spec section 9 constraint 7).
const MaxStatelessHistoryMessages = 100

// requestRegistryEntry stores the transient session/message IDs for a given
// request_id. Used by the stop endpoint to locate the right stream.
type requestRegistryEntry struct {
	sessionID string
	messageID string
}

// requestRegistry maps request_id -> {sessionID, messageID} for active
// stateless QA streams. Cleaned up when the stream ends.
var requestRegistry sync.Map

// KnowledgeQAStateless handles stateless knowledge QA requests.
// POST /api/v1/knowledge-chat-stateless
func (h *Handler) KnowledgeQAStateless(c *gin.Context) {
	receivedAt := time.Now()
	ctx := logger.CloneContext(c.Request.Context())
	requestID := uuid.New().String()

	// Tenant context is required (spec section 5.1: 401 for unauthenticated)
	_, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Tenant ID not found in context")
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	// Check request body size before binding (spec section 5.1: 413)
	if c.Request.ContentLength > MaxStatelessRequestBody {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"success": false,
			"error":   gin.H{"code": "PAYLOAD_TOO_LARGE", "message": "request body exceeds 10 MB limit"},
		})
		return
	}
	// Also enforce on read
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxStatelessRequestBody)

	// Parse request body
	var request CreateKnowledgeQAStatelessRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		if c.Request.Body != nil {
			if _, readErr := io.Copy(io.Discard, c.Request.Body); readErr != nil {
				// MaxBytesReader returned an error
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{
					"success": false,
					"error":   gin.H{"code": "PAYLOAD_TOO_LARGE", "message": "request body exceeds 10 MB limit"},
				})
				return
			}
		}
		logger.Error(ctx, "Failed to parse stateless QA request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// Validate query is not empty (spec section 2.2)
	if request.Query == "" {
		c.Error(errors.NewBadRequestError("Query content cannot be empty"))
		return
	}

	// Validate knowledge_ids requires knowledge_base_ids (spec section 2.2)
	if len(request.KnowledgeIDs) > 0 && len(request.KnowledgeBaseIDs) == 0 {
		c.Error(errors.NewBadRequestError("knowledge_ids cannot be provided without knowledge_base_ids"))
		return
	}

	// Validate tag_ids requires knowledge_base_ids (spec section 2.2)
	if len(request.TagIDs) > 0 && len(request.KnowledgeBaseIDs) == 0 {
		c.Error(errors.NewBadRequestError("tag_ids cannot be provided without knowledge_base_ids"))
		return
	}

	// Validate history message count (spec section 9 constraint 7: max 100)
	if len(request.History) > MaxStatelessHistoryMessages {
		c.Error(errors.NewBadRequestError(
			fmt.Sprintf("history exceeds maximum of %d messages", MaxStatelessHistoryMessages),
		))
		return
	}

	// Validate history role: only user and assistant allowed (spec section 2.2)
	for i, msg := range request.History {
		if msg.Role != "user" && msg.Role != "assistant" {
			c.Error(errors.NewBadRequestError(
				fmt.Sprintf("history[%d].role must be 'user' or 'assistant', got %q", i, msg.Role),
			))
			return
		}
	}

	// Validate attachment size (spec section 2.2: max 5 MB per file)
	for i, upload := range request.AttachmentUploads {
		if upload.FileSize > 5*1024*1024 {
			c.Error(errors.NewBadRequestError(
				fmt.Sprintf("attachment_uploads[%d] exceeds 5 MB limit", i),
			))
			return
		}
	}

	// SSRF protection on images (mirror existing handler)
	for i := range request.Images {
		request.Images[i].URL = ""
		request.Images[i].Caption = ""
	}

	// Resolve summary_model_id (spec section 2.2)
	// Priority: UUID exact match first, then name-based lookup.
	// If not provided, use the tenant's default KnowledgeQA model.
	summaryModelID, modelErr := h.resolveSummaryModel(ctx, tenantID, request.SummaryModelID)
	if modelErr != nil {
		logger.Errorf(ctx, "Stateless QA: model resolution failed for %q: %v",
			secutils.SanitizeForLog(request.SummaryModelID), modelErr)
		// Spec section 5.1: nonexistent model returns 403
		c.Error(errors.NewForbiddenError("model not found or not accessible"))
		return
	}

	// Validate KB access (spec section 5.1: nonexistent KB returns 403)
	for _, kbID := range request.KnowledgeBaseIDs {
		if h.knowledgebaseService != nil {
			if _, err := h.knowledgebaseService.GetKnowledgeBaseByID(ctx, kbID); err != nil {
				logger.Warnf(ctx, "Stateless QA: KB %q not found or not accessible: %v",
					secutils.SanitizeForLog(kbID), err)
				c.Error(errors.NewForbiddenError("knowledge base not found or not accessible"))
				return
			}
		}
	}

	// Build temporary session and message IDs for StreamManager
	sessionID := uuid.New().String()
	assistantMessageID := uuid.New().String()

	// Register request_id mapping for stop (spec section 6)
	requestRegistry.Store(requestID, requestRegistryEntry{
		sessionID: sessionID,
		messageID: assistantMessageID,
	})

	// Construct transient session (not persisted per spec section 7)
	transientSession := &types.Session{
		ID:       sessionID,
		TenantID: tenantID,
	}

	// Build QA request
	qaReq := &types.QARequest{
		Session:            transientSession,
		Query:              request.Query,
		AssistantMessageID: assistantMessageID,
		SummaryModelID:     summaryModelID,
		KnowledgeBaseIDs:   request.KnowledgeBaseIDs,
		KnowledgeIDs:       request.KnowledgeIDs,
		WebSearchEnabled:   request.WebSearchEnabled,
		SystemPrompt:       request.SystemPrompt,
	}

	logger.Infof(ctx, "Stateless QA: request_id=%s session=%s query=%s kb=%v model=%s",
		requestID, sessionID, secutils.SanitizeForLog(request.Query),
		secutils.SanitizeForLogArray(request.KnowledgeBaseIDs), summaryModelID)

	// Set SSE headers
	setSSEHeaders(c)

	// Write agent_query event first (spec section 3.1, 3.2)
	writeSSEEvent(c, "agent_query", map[string]interface{}{
		"request_id": requestID,
		"query":      request.Query,
	})
	c.Writer.Flush()

	// Base context for async work
	baseCtx := logger.CloneContext(ctx)

	// Create EventBus and cancellable context
	eventBus := event.NewEventBus()
	asyncCtx, cancel := context.WithCancel(baseCtx)

	// Deferred cleanup
	defer func() {
		requestRegistry.Delete(requestID)
		cancel()
	}()

	// Setup stop event handler
	eventBus.On(event.EventStop, func(ctx context.Context, evt event.Event) error {
		logger.Infof(ctx, "Stateless QA: stop event received for request=%s", requestID)
		cancel()
		return nil
	})

	// Register handler to emit EventAgentComplete when final answer is done
	// (the AgentStreamHandler subscribes to EventAgentComplete to produce
	// the "complete" StreamEvent that terminates the SSE loop)
	var completionHandled bool
	eventBus.On(event.EventAgentFinalAnswer, func(ctx context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentFinalAnswerData)
		if !ok {
			return nil
		}
		if data.Done && !completionHandled {
			completionHandled = true
			logger.Infof(ctx, "Stateless QA: final answer done for request=%s", requestID)
			_ = eventBus.Emit(ctx, event.Event{
				Type: event.EventAgentComplete,
				Data: event.AgentCompleteData{FinalAnswer: data.Content},
			})
		}
		return nil
	})

	// Start stop watcher
	h.startStopWatcher(logger.CloneContext(baseCtx), sessionID, assistantMessageID, eventBus)

	// Setup stream handler
	h.setupStreamHandler(asyncCtx, sessionID, assistantMessageID, requestID, receivedAt,
		&types.Message{
			SessionID:   sessionID,
			Role:        "assistant",
			RequestID:   requestID,
			IsCompleted: false,
		}, eventBus)

	// Execute QA asynchronously
	go func() {
		defer func() {
			if r := recover(); r != nil {
				buf := make([]byte, 10240)
				runtime.Stack(buf, true)
				logger.ErrorWithFields(asyncCtx,
					errors.NewInternalServerError(
						fmt.Sprintf("Stateless QA panicked: %v\n%s", r, string(buf)),
					), map[string]interface{}{"request_id": requestID})
			}
		}()

		if err := h.sessionService.KnowledgeQA(asyncCtx, qaReq, eventBus); err != nil {
			if asyncCtx.Err() != nil {
				logger.Infof(asyncCtx, "Stateless QA cancelled by user stop for request=%s", requestID)
			} else {
				logger.ErrorWithFields(asyncCtx, err, map[string]interface{}{
					"request_id": requestID,
				})
				eventBus.Emit(asyncCtx, event.Event{
					Type: event.EventError,
					Data: event.ErrorData{
						Error: err.Error(),
						Stage: "knowledge_qa_execution",
					},
				})
			}
		}
	}()

	// Poll StreamManager and write SSE in spec format
	h.handleStatelessSSE(baseCtx, c, sessionID, assistantMessageID, requestID, summaryModelID,
		eventBus, receivedAt)
}

// StopStatelessQA handles stop requests for stateless QA.
// POST /api/v1/knowledge-chat-stateless/stop (spec section 6)
func (h *Handler) StopStatelessQA(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	var req StopStatelessQARequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Errorf(ctx, "Stateless QA stop: failed to parse request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "request_id is required"})
		return
	}

	requestID := secutils.SanitizeForLog(req.RequestID)
	logger.Infof(ctx, "Stateless QA stop: request_id=%s", requestID)

	// Look up the request_id mapping
	entryVal, ok := requestRegistry.Load(requestID)
	if !ok {
		// Idempotent per spec section 6.3: return 200 for unknown request IDs
		logger.Infof(ctx, "Stateless QA stop: request_id=%s not found (already completed or unknown)", requestID)
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "stop acknowledged"})
		return
	}
	entry := entryVal.(requestRegistryEntry)

	// Write stop event to StreamManager
	stopEvent := interfaces.StreamEvent{
		ID:        fmt.Sprintf("stop-%d", time.Now().UnixNano()),
		Type:      types.ResponseType(event.EventStop),
		Content:   "",
		Done:      true,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"session_id": entry.sessionID,
			"message_id": entry.messageID,
			"reason":     "user_requested",
		},
	}

	if err := h.streamManager.AppendEvent(ctx, entry.sessionID, entry.messageID, stopEvent); err != nil {
		logger.Errorf(ctx, "Stateless QA stop: failed to write stop event for request=%s: %v", requestID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to stop"})
		return
	}

	logger.Infof(ctx, "Stateless QA stop: stop event written for request=%s", requestID)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "stop acknowledged"})
}

// handleStatelessSSE polls StreamManager and writes SSE events in the format
// defined by the stateless chat spec (sections 3.1, 3.2).
func (h *Handler) handleStatelessSSE(
	ctx context.Context,
	c *gin.Context,
	sessionID, assistantMessageID, requestID, modelID string,
	eventBus *event.EventBus,
	receivedAt time.Time,
) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	lastOffset := 0
	log := logger.GetLogger(ctx)
	log.Infof("Stateless SSE: starting for request=%s session=%s message=%s",
		requestID, sessionID, assistantMessageID)

	var answerContent strings.Builder
	var references []interface{}

	for {
		select {
		case <-c.Request.Context().Done():
			log.Infof("Stateless SSE: client disconnected for request=%s", requestID)
			return

		case <-ticker.C:
			events, newOffset, err := h.streamManager.GetEvents(ctx, sessionID, assistantMessageID, lastOffset)
			if err != nil {
				log.Warnf("Stateless SSE: get events error: %v", err)
				continue
			}

			completed := false
			for _, evt := range events {
				if evt.Type == types.ResponseType(event.EventStop) {
					log.Infof("Stateless SSE: stop event detected for request=%s", requestID)
					if eventBus != nil {
						_ = eventBus.Emit(ctx, event.Event{
							Type: event.EventStop,
							Data: event.StopData{
								SessionID: sessionID,
								MessageID: assistantMessageID,
								Reason:    "user_requested",
							},
						})
					}
					// Emit final_answer with accumulated content (spec section 6.3)
					writeSSEEvent(c, "final_answer", map[string]interface{}{
						"content":    answerContent.String(),
						"done":       true,
						"references": references,
					})
					c.Writer.Flush()
					writeSSEEvent(c, "complete", map[string]interface{}{
						"request_id": requestID,
						"model_id":   modelID,
						"elapsed_ms": time.Since(receivedAt).Milliseconds(),
					})
					c.Writer.Flush()
					return
				}

				if convertAndWriteStatelessSSE(c, evt, requestID, modelID, receivedAt, &references, &answerContent) {
					completed = true
				}
			}

			lastOffset = newOffset

			if completed {
				return
			}
		}
	}
}

// convertAndWriteStatelessSSE converts a single StreamEvent to the spec SSE
// format and writes it. Returns true if the stream has completed.
func convertAndWriteStatelessSSE(
	c *gin.Context,
	evt interfaces.StreamEvent,
	requestID, modelID string,
	receivedAt time.Time,
	references *[]interface{},
	answerContent *strings.Builder,
) bool {
	switch evt.Type {
	case "answer":
		writeSSEEvent(c, "answer", map[string]interface{}{
			"delta": evt.Content,
		})
		if evt.Content != "" {
			answerContent.WriteString(evt.Content)
		}

	case "tool_call":
		data := make(map[string]interface{})
		if evt.Data != nil {
			if v, ok := evt.Data["tool_call_id"]; ok {
				data["tool_call_id"] = v
			}
			if v, ok := evt.Data["tool_name"]; ok {
				data["tool_name"] = v
			}
			if v, ok := evt.Data["arguments"]; ok {
				data["arguments"] = v
			}
		}
		writeSSEEvent(c, "tool_call", data)

	case "tool_result":
		data := make(map[string]interface{})
		if evt.Data != nil {
			if v, ok := evt.Data["tool_call_id"]; ok {
				data["tool_call_id"] = v
			}
			if v, ok := evt.Data["tool_name"]; ok {
				data["tool_name"] = v
			}
		}
		output := make(map[string]interface{})
		if evt.Data != nil {
			if v, ok := evt.Data["output"]; ok {
				// Parse the output JSON string and merge its fields
				if s, ok := v.(string); ok {
					var parsed map[string]interface{}
					if json.Unmarshal([]byte(s), &parsed) == nil {
						for k, val := range parsed {
							output[k] = val
						}
					} else {
						output["result"] = s
					}
				} else {
					output["result"] = v
				}
			}
			if v, ok := evt.Data["duration"]; ok {
				output["total_duration_ms"] = v
			}
		}
		data["output"] = output
		data["references"] = *references
		writeSSEEvent(c, "tool_result", data)

	case "references":
		if evt.Data != nil {
			if refs, ok := evt.Data["references"]; ok {
				switch r := refs.(type) {
				case []interface{}:
					*references = r
				case types.References:
					// types.References is []*SearchResult
					slice := make([]interface{}, len(r))
					for i, sr := range r {
						slice[i] = sr
					}
					*references = slice
				}
			}
		}

	case "complete":
		// Emit final_answer with accumulated content (spec section 3.1)
		writeSSEEvent(c, "final_answer", map[string]interface{}{
			"content":    answerContent.String(),
			"done":       true,
			"references": *references,
		})
		c.Writer.Flush()
		completeData := map[string]interface{}{
			"request_id": requestID,
			"model_id":   modelID,
			"elapsed_ms": time.Since(receivedAt).Milliseconds(),
		}
		// Include usage if available from the StreamEvent data
		if evt.Data != nil {
			if usage, ok := evt.Data["usage"]; ok {
				completeData["usage"] = usage
			} else {
				completeData["usage"] = map[string]interface{}{
					"prompt_tokens":     evt.Data["prompt_tokens"],
					"completion_tokens": evt.Data["completion_tokens"],
					"total_tokens":      evt.Data["total_tokens"],
				}
			}
		} else {
			completeData["usage"] = map[string]interface{}{
				"prompt_tokens":     0,
				"completion_tokens": 0,
				"total_tokens":      0,
			}
		}
		writeSSEEvent(c, "complete", completeData)
		return true

	case "error":
		data := map[string]interface{}{
			"request_id": requestID,
		}
		if evt.Data != nil {
			if v, ok := evt.Data["error_code"]; ok {
				data["code"] = v
			} else if v, ok := evt.Data["code"]; ok {
				data["code"] = v
			}
			if v, ok := evt.Data["error"]; ok {
				data["message"] = v
			} else if v, ok := evt.Data["message"]; ok {
				data["message"] = v
			} else {
				data["message"] = evt.Content
			}
		}
		writeSSEEvent(c, "error", data)
		return true
	}

	c.Writer.Flush()
	return false
}

// resolveSummaryModel resolves a summary_model_id to a concrete model ID.
// Resolution order: UUID exact match, then name-based lookup.
// If empty, returns the tenant's default KnowledgeQA model.
func (h *Handler) resolveSummaryModel(ctx context.Context, tenantID uint64, input string) (string, error) {
	if input == "" {
		// Find default KnowledgeQA model for the tenant
		if h.modelService != nil {
			models, err := h.modelService.ListModels(ctx)
			if err != nil {
				return "", err
			}
			for _, m := range models {
				if m.Type == types.ModelTypeKnowledgeQA && m.IsDefault {
					return m.ID, nil
				}
			}
			// Fallback: first KnowledgeQA model
			for _, m := range models {
				if m.Type == types.ModelTypeKnowledgeQA {
					return m.ID, nil
				}
			}
		}
		return "", fmt.Errorf("no default KnowledgeQA model configured")
	}

	if h.modelService == nil {
		return input, nil
	}

	// Try UUID exact match first
	model, err := h.modelService.GetModelByID(ctx, input)
	if err == nil && model != nil {
		return model.ID, nil
	}

	// Try name-based lookup
	models, listErr := h.modelService.ListModels(ctx)
	if listErr != nil {
		return "", fmt.Errorf("model resolution failed: %w", listErr)
	}
	for _, m := range models {
		if m.Name == input && m.Type == types.ModelTypeKnowledgeQA {
			return m.ID, nil
		}
	}

	return "", fmt.Errorf("model not found: %s", input)
}

// writeSSEEvent writes a named SSE event in the spec format:
//
//	event: <eventType>
//	data: <jsonString>
func writeSSEEvent(c *gin.Context, eventType string, data map[string]interface{}) {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		logger.GetLogger(c.Request.Context()).Errorf(
			"Stateless SSE: failed to marshal event data for %s: %v", eventType, err)
		return
	}
	_, _ = c.Writer.Write([]byte(fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, string(jsonBytes))))
}
