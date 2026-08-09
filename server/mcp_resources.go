package server

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MCP Resources.
//
// Tools are model-controlled: the agent decides to call them. Resources are *application*-driven —
// the host surfaces them for a person to pick, which is what makes `@`-mentioning a wiki page in
// Claude Desktop or Cursor work. That path costs no tool call and no tokens spent on tool-result
// prose, so exposing the wiki as resources is a different affordance from read_article, not a
// duplicate of it.

// ResourceURIScheme identifies NexWiki article resources.
//
// A custom scheme rather than file://: the spec reserves file:// for things a client may treat as
// a real filesystem, and an article's identity here is its slug, not its path on disk. Encoding
// the on-disk path would also leak the data directory layout into every client.
const ResourceURIScheme = "nexwiki"

// resourceURIPrefix is the full prefix of every article resource URI.
const resourceURIPrefix = ResourceURIScheme + "://article/"

// articleResourceURI builds the canonical resource URI for a slug.
func articleResourceURI(slug string) string {
	return resourceURIPrefix + slug
}

// slugFromResourceURI extracts the article slug from a resource URI, reporting whether the URI is
// one this server serves.
func slugFromResourceURI(uri string) (string, bool) {
	if !strings.HasPrefix(uri, resourceURIPrefix) {
		return "", false
	}
	slug := strings.TrimPrefix(uri, resourceURIPrefix)
	if slug == "" {
		return "", false
	}
	return slug, true
}

// resourceCapability describes the resources feature NexWiki implements. Both sub-features are
// genuinely supported: the article set changes as documents are created and deleted, and
// individual articles change as they are edited.
func resourceCapability() map[string]interface{} {
	return map[string]interface{}{
		"listChanged": true,
		"subscribe":   true,
	}
}

// listResources projects every article into a Resource entry.
//
// "home" is included here even though ListArticles excludes it: the dashboard exclusion exists so
// the sidebar does not show it as an ordinary page, but as a resource a user may well want to
// @-mention it.
func (srv *Server) listResources() (interface{}, *JSONRPCError) {
	articles, err := srv.Storage.ListArticles()
	if err != nil {
		return nil, &JSONRPCError{Code: errCodeInternal, Message: "failed to list articles: " + err.Error()}
	}
	if home, err := srv.Storage.GetArticle("home"); err == nil {
		articles = append([]Article{*home}, articles...)
	}

	resources := make([]map[string]interface{}, 0, len(articles))
	for _, art := range articles {
		entry := map[string]interface{}{
			"uri":      articleResourceURI(art.Slug),
			"name":     art.Slug,
			"title":    art.Title,
			"mimeType": "text/markdown",
			// lastModified lets a client sort by recency or show staleness without reading each one.
			"annotations": map[string]interface{}{
				"audience":     []string{"user", "assistant"},
				"lastModified": art.Timestamp.UTC().Format("2006-01-02T15:04:05Z"),
			},
		}
		if description := art.Description; description != "" {
			entry["description"] = description
		} else if art.ContentPreview != "" {
			entry["description"] = art.ContentPreview
		}
		resources = append(resources, entry)
	}

	return map[string]interface{}{"resources": resources}, nil
}

// listResourceTemplates advertises the URI shape, so a client can construct a resource URI for a
// slug it already knows rather than paging the whole list to find it.
func (srv *Server) listResourceTemplates() (interface{}, *JSONRPCError) {
	return map[string]interface{}{
		"resourceTemplates": []map[string]interface{}{
			{
				"uriTemplate": resourceURIPrefix + "{slug}",
				"name":        "wiki-article",
				"title":       "NexWiki Article",
				"description": "Any NexWiki document by its URL-safe slug — wiki articles, agent memories, plans, and skills.",
				"mimeType":    "text/markdown",
			},
		},
	}, nil
}

// readResource returns an article's Markdown body.
//
// A missing resource is -32602 with the URI echoed in data, per the resources spec. Returning an
// empty contents array instead would be ambiguous — it cannot distinguish "exists but empty" from
// "does not exist" — and the spec explicitly forbids it.
func (srv *Server) readResource(params json.RawMessage) (interface{}, *JSONRPCError) {
	var args struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &args); err != nil || args.URI == "" {
		return nil, &JSONRPCError{Code: errCodeInvalidParams, Message: "Missing or invalid 'uri' argument"}
	}

	slug, ok := slugFromResourceURI(args.URI)
	if !ok {
		return nil, &JSONRPCError{
			Code:    errCodeInvalidParams,
			Message: fmt.Sprintf("Unsupported resource URI: %s (expected %s{slug})", args.URI, resourceURIPrefix),
			Data:    map[string]interface{}{"uri": args.URI},
		}
	}

	art, err := srv.Storage.GetArticle(slug)
	if err != nil {
		return nil, &JSONRPCError{
			Code:    errCodeInvalidParams,
			Message: "Resource not found",
			Data:    map[string]interface{}{"uri": args.URI},
		}
	}

	return map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"uri":      args.URI,
				"mimeType": "text/markdown",
				"text":     art.Content,
			},
		},
	}, nil
}
