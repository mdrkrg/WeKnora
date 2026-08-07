package session

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// MaxKnowledgeRetrieveRequestBody is the maximum request body size for the
// retrieve endpoint (spec section 7.1: 10 MB).
const MaxKnowledgeRetrieveRequestBody = 10 * 1024 * 1024

type knowledgeRetrieveService interface {
	RetrieveKnowledge(context.Context, *types.KnowledgeRetrieveRequest) (*types.KnowledgeRetrieveData, error)
}

// KnowledgeRetrieve handles POST /api/v1/knowledge-retrieve.
func (h *Handler) KnowledgeRetrieve(c *gin.Context) {
	if _, ok := c.Get(types.TenantIDContextKey.String()); !ok {
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}
	if c.Request.ContentLength > MaxKnowledgeRetrieveRequestBody {
		c.Error(&errors.AppError{Code: errors.ErrValidation + 1, Message: "request body exceeds 10 MB limit", HTTPCode: http.StatusRequestEntityTooLarge})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxKnowledgeRetrieveRequestBody)

	var wire KnowledgeRetrieveWireRequest
	if err := c.ShouldBindJSON(&wire); err != nil {
		if _, readErr := io.Copy(io.Discard, c.Request.Body); readErr != nil {
			c.Error(&errors.AppError{Code: errors.ErrValidation + 1, Message: "request body exceeds 10 MB limit", HTTPCode: http.StatusRequestEntityTooLarge})
			return
		}
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if err := ValidateKnowledgeRetrieveRequest(wire); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	service, ok := h.sessionService.(knowledgeRetrieveService)
	if !ok {
		c.Error(errors.NewInternalServerError("knowledge retrieve service unavailable"))
		return
	}

	request := wire.toServiceRequest()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
	defer cancel()
	data, err := service.RetrieveKnowledge(ctx, &request)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			c.Error(&errors.AppError{Code: errors.ErrTimeout, Message: "knowledge retrieve request timed out", HTTPCode: http.StatusGatewayTimeout})
			return
		}
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	if data == nil {
		data = &types.KnowledgeRetrieveData{RewriteQuery: strings.TrimSpace(request.Query), Intent: types.IntentKBSearch, Results: []*types.KnowledgeRetrieveResult{}}
	}
	if data.Results == nil {
		data.Results = []*types.KnowledgeRetrieveResult{}
	}
	c.JSON(http.StatusOK, types.KnowledgeRetrieveResponse{Success: true, Data: data})
}
