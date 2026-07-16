package handler

import (
	"mime"
	"net/http"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// ReadArtifact godoc
// @Summary      Read artifact content
// @Description  Returns the content and metadata of a parsed artifact (markdown, image_manifest, engine_native).
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id              path      string  true   "知识ID"
// @Param        type            query     string  false  "产物类型 (default: markdown)"
// @Param        native_kind     query     string  false  "引擎原生产物种类 (only when type=engine_native)"
// @Param        attempt         query     int     false  "尝试编号 (0=current, default: 0)"
// @Param        resolve_images  query     bool    false  "解析图片URL为预签名HTTP地址"
// @Success      200             {object}  types.ArtifactReadResponse
// @Failure      400,404,403     {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id}/artifact [get]
func (h *KnowledgeHandler) ReadArtifact(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	if id == "" {
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	var req types.ArtifactReadRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.Error(errors.NewBadRequestError("Invalid query parameters").WithDetails(err.Error()))
		return
	}
	if req.Type == "" {
		req.Type = types.ArtifactTypeMarkdown
	}

	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleViewer)
	if err != nil {
		c.Error(err)
		return
	}

	result, err := h.kgService.ReadArtifact(effCtx, id, req)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, result)
}

// ListArtifacts godoc
// @Summary      List artifacts
// @Description  Returns metadata for all artifacts under a knowledge/attempt (no content body).
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id       path      string  true   "知识ID"
// @Param        attempt  query     int     false  "尝试编号 (0=current, default: 0)"
// @Success      200      {array}   types.ArtifactListItem
// @Failure      400,404,403     {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id}/artifacts [get]
func (h *KnowledgeHandler) ListArtifacts(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	if id == "" {
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	var req types.ArtifactListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.Error(errors.NewBadRequestError("Invalid query parameters").WithDetails(err.Error()))
		return
	}

	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleViewer)
	if err != nil {
		c.Error(err)
		return
	}

	result, err := h.kgService.ListArtifacts(effCtx, id, req)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, result)
}

// DownloadArtifact godoc
// @Summary      Download artifact content
// @Description  Streams the full artifact content (no size limit).
// @Tags         知识管理
// @Accept       json
// @Produce      application/octet-stream,text/markdown,application/json
// @Param        id              path      string  true   "知识ID"
// @Param        type            query     string  false  "产物类型 (default: markdown)"
// @Param        native_kind     query     string  false  "引擎原生产物种类"
// @Param        attempt         query     int     false  "尝试编号"
// @Param        resolve_images  query     bool    false  "解析图片URL"
// @Success      200             {file}    file    "产物内容"
// @Failure      400,404,403     {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id}/artifact/download [get]
func (h *KnowledgeHandler) DownloadArtifact(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	if id == "" {
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	var req types.ArtifactReadRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.Error(errors.NewBadRequestError("Invalid query parameters").WithDetails(err.Error()))
		return
	}
	if req.Type == "" {
		req.Type = types.ArtifactTypeMarkdown
	}

	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleViewer)
	if err != nil {
		c.Error(err)
		return
	}

	reader, ct, err := h.kgService.DownloadArtifact(effCtx, id, req)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	defer reader.Close()

	if ct == "" {
		ct = "application/octet-stream"
	}
	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Transfer-Encoding", "binary")
	c.Header("Content-Type", ct)
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "artifact"}))
	c.Header("Cache-Control", "no-cache")
	dataLen := reader
	c.DataFromReader(http.StatusOK, -1, ct, dataLen, nil)
}
