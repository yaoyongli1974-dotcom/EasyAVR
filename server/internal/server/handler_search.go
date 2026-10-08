package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type searchRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"topK"`
}

// semanticSearch answers a natural-language query over AI events, using vector
// similarity when an embedding provider is configured, else keyword matching.
func (a *App) semanticSearch(c *gin.Context) {
	var req searchRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Query == "" {
		fail(c, http.StatusBadRequest, "query is required")
		return
	}
	hits := a.search.Search(req.Query, req.TopK)
	ok(c, gin.H{
		"hits": hits, "semantic": a.search.HasEmbedder(),
		"backend": a.search.VectorBackend(), "count": len(hits),
	})
}
