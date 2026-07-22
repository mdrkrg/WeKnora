package session

import (
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
)

const MaxKnowledgeRetrieveHistory = 100

type KnowledgeRetrieveRequest = types.KnowledgeRetrieveRequest
type KnowledgeRetrieveData = types.KnowledgeRetrieveData
type KnowledgeRetrieveResult = types.KnowledgeRetrieveResult
type KnowledgeRetrieveResponse = types.KnowledgeRetrieveResponse
type KnowledgeRetrieveError = types.KnowledgeRetrieveError

// ValidateKnowledgeRetrieveRequest is the test seam for request validation.
// The implementation is intentionally left as a TDD stub in the first cycle.
func ValidateKnowledgeRetrieveRequest(types.KnowledgeRetrieveRequest) error {
	return errors.New("knowledge retrieve validation not implemented")
}

// KnowledgeRetrieveMatchType is the response-layer MatchType conversion seam.
func KnowledgeRetrieveMatchType(types.MatchType) string { return "" }

func KnowledgeRetrieveErrorEnvelope(code int, message string) types.KnowledgeRetrieveResponse {
	return types.KnowledgeRetrieveResponse{
		Success: false,
		Error:   &types.KnowledgeRetrieveError{Code: code, Message: message, Details: nil},
	}
}
