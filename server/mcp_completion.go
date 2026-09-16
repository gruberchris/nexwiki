package server

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// completion/complete — argument autocompletion for prompts and resource templates.
//
// This is what makes the resource template usable. NexWiki advertises
// `nexwiki://article/{slug}` so a client can build a URI for a slug it already knows, but until now
// there was no way to *discover* the slug except by paging the whole resource list. Completion
// closes that: a host filling in `{slug}` asks the server, and gets the matching documents back.
//
// Both eras serve it from here, for the same reason prompts are shared — a feature advertised by
// one era and missing from the other is worse than one missing from both.

// maxCompletionValues is the ceiling the specification places on a single completion response.
const maxCompletionValues = 100

// completionRequest is the params object of a completion/complete request.
type completionRequest struct {
	Ref struct {
		Type string `json:"type"`
		Name string `json:"name"`
		URI  string `json:"uri"`
	} `json:"ref"`
	Argument struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"argument"`
	Context struct {
		Arguments map[string]string `json:"arguments"`
	} `json:"context"`
}

// resourceTemplateSlugArgument is the single variable in the article URI template, and therefore
// the only argument name a ref/resource completion against it can be asking about.
const resourceTemplateSlugArgument = "slug"

// complete answers completion/complete.
//
// An unknown prompt or an unrecognized reference is -32602: the client named something that does
// not exist, which is a bad argument, not a missing method. An argument we simply have no
// suggestions for is *not* an error — it returns an empty value list, because "nothing to suggest"
// is a legitimate answer and a client typing into a free-text field should not see a failure.
func (srv *Server) complete(params json.RawMessage) (interface{}, *JSONRPCError) {
	var req completionRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &JSONRPCError{Code: errCodeInvalidParams, Message: "Invalid completion parameters"}
	}

	var values []string
	var rpcErr *JSONRPCError

	switch req.Ref.Type {
	case "ref/resource":
		values, rpcErr = srv.completeResourceArgument(req)
	case "ref/prompt":
		values, rpcErr = srv.completePromptArgument(req)
	default:
		return nil, &JSONRPCError{
			Code:    errCodeInvalidParams,
			Message: fmt.Sprintf("Unsupported completion reference type: %q (expected ref/prompt or ref/resource)", req.Ref.Type),
		}
	}
	if rpcErr != nil {
		return nil, rpcErr
	}

	return completionResult(values), nil
}

// completionResult renders values as a CompleteResult, truncating to the specification's ceiling.
// total counts every match, so a client can tell "these are all of them" from "these are the first
// hundred" — which is exactly what hasMore reports.
func completionResult(values []string) map[string]interface{} {
	total := len(values)
	hasMore := total > maxCompletionValues
	if hasMore {
		values = values[:maxCompletionValues]
	}
	if values == nil {
		values = []string{}
	}
	return map[string]interface{}{
		"completion": map[string]interface{}{
			"values":  values,
			"total":   total,
			"hasMore": hasMore,
		},
	}
}

// completeResourceArgument suggests article slugs for the `{slug}` variable of the article URI
// template. A URI that is not this server's template is -32602 rather than an empty list, so a
// client completing against the wrong server learns that instead of seeing a page with no articles.
func (srv *Server) completeResourceArgument(req completionRequest) ([]string, *JSONRPCError) {
	if !isArticleURITemplate(req.Ref.URI) {
		return nil, &JSONRPCError{
			Code:    errCodeInvalidParams,
			Message: fmt.Sprintf("Unknown resource template: %s (expected %s{slug})", req.Ref.URI, resourceURIPrefix),
			Data:    map[string]interface{}{"uri": req.Ref.URI},
		}
	}
	if req.Argument.Name != resourceTemplateSlugArgument {
		return nil, nil
	}

	articles, err := srv.Storage.ListArticles()
	if err != nil {
		return nil, &JSONRPCError{Code: errCodeInternal, Message: "failed to list articles: " + err.Error()}
	}

	slugs := make([]string, 0, len(articles)+1)
	// "home" is excluded from ListArticles for the sidebar's sake but is a perfectly good resource.
	if home, err := srv.Storage.GetArticle("home"); err == nil {
		slugs = append(slugs, home.Slug)
	}
	for _, art := range articles {
		slugs = append(slugs, art.Slug)
	}
	return rankCompletions(slugs, req.Argument.Value), nil
}

// isArticleURITemplate reports whether a ref/resource URI addresses this server's article
// template. Both the template itself and a partially-filled concrete URI are accepted, since a host
// completing a URI in place sends whatever the user has typed so far.
func isArticleURITemplate(uri string) bool {
	return uri == resourceURIPrefix+"{"+resourceTemplateSlugArgument+"}" ||
		strings.HasPrefix(uri, resourceURIPrefix)
}

// completePromptArgument suggests values for a prompt's arguments.
//
// The suggestions come from the wiki itself rather than a fixed list, which is the point: a plan's
// project context should complete to projects that already exist, so an agent reuses one instead of
// coining a near-duplicate and splitting the plan history in two.
func (srv *Server) completePromptArgument(req completionRequest) ([]string, *JSONRPCError) {
	if !promptExists(req.Ref.Name) {
		return nil, &JSONRPCError{
			Code:    errCodeInvalidParams,
			Message: fmt.Sprintf("Prompt not found: %s", req.Ref.Name),
		}
	}

	articles, err := srv.Storage.ListArticles()
	if err != nil {
		return nil, &JSONRPCError{Code: errCodeInternal, Message: "failed to list articles: " + err.Error()}
	}

	switch req.Argument.Name {
	case "title":
		titles := make([]string, 0, len(articles))
		for _, art := range articles {
			titles = append(titles, art.Title)
		}
		return rankCompletions(titles, req.Argument.Value), nil

	case "project":
		return rankCompletions(planProjectContexts(articles), req.Argument.Value), nil

	default:
		// A free-text argument such as `description` has nothing to suggest from.
		return nil, nil
	}
}

// promptExists reports whether a name matches one of the served prompts, reading the same
// definitions prompts/list emits so the two can never disagree.
func promptExists(name string) bool {
	for _, prompt := range promptDefinitions() {
		if prompt["name"] == name {
			return true
		}
	}
	return false
}

// planProjectContexts collects the distinct project contexts already in use across plan documents.
//
// create_agent_plan slugifies project_context onto the plan as a tag, so the project is recovered
// by discarding any tag that names a lifecycle state. knownStatusWords is the same vocabulary the
// tag validator uses to reject a status word in a plan's tags, so the two cannot drift apart.
func planProjectContexts(articles []Article) []string {
	seen := map[string]bool{}
	var contexts []string
	for _, art := range articles {
		if art.Type != ContentTypePlan {
			continue
		}
		for _, tag := range art.Tags {
			candidate := strings.TrimSpace(tag)
			lower := strings.ToLower(candidate)
			if candidate == "" || knownStatusWords[lower] || seen[lower] {
				continue
			}
			seen[lower] = true
			contexts = append(contexts, candidate)
		}
	}
	sort.Strings(contexts)
	return contexts
}

// rankCompletions filters candidates against what the user has typed and orders them so the most
// likely answer is first.
//
// Prefix matches rank above substring matches because that is what someone typing a slug means: a
// person who has typed "go-" wants the slugs that start with it, not every page that mentions Go
// somewhere in its name. Within each band the order is alphabetical, so the list is stable across
// calls and a client can cache it.
func rankCompletions(candidates []string, typed string) []string {
	needle := strings.ToLower(strings.TrimSpace(typed))

	var prefix, contains []string
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true

		lower := strings.ToLower(candidate)
		switch {
		case needle == "" || strings.HasPrefix(lower, needle):
			prefix = append(prefix, candidate)
		case strings.Contains(lower, needle):
			contains = append(contains, candidate)
		}
	}

	sort.Strings(prefix)
	sort.Strings(contains)
	return append(prefix, contains...)
}
