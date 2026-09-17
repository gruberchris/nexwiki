package server

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// Cursor pagination for the four list operations the specification says support it:
// tools/list, prompts/list, resources/list, and resources/templates/list.
//
// Only resources/list realistically pages — the tool, prompt, and template sets are compiled into
// the binary and comfortably fit one page, while a knowledge base is unbounded and used to be
// projected into a single response no matter how large it grew. Routing all four through the same
// helper is deliberate anyway: it means an invalid cursor is rejected identically everywhere, so a
// client cannot discover that one list validates its cursor and another silently ignores it.

// listPageSize is the number of entries in one page. Clients MUST NOT assume a fixed page size, so
// this is free to change; it is large enough that neither the tools nor the prompts list is ever
// split, and small enough that a single resources/list response stays a reasonable size.
const listPageSize = 100

// cursorPrefix namespaces the encoded offset. It is not a security measure — cursors are opaque to
// clients, not secret — but it does mean a cursor minted by some other server cannot be mistaken
// for one of ours and silently decoded as a plausible offset.
const cursorPrefix = "nexwiki:"

// encodeCursor renders a position in the result set as the opaque token clients echo back.
func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(cursorPrefix + strconv.Itoa(offset)))
}

// decodeCursor reverses encodeCursor. Anything it cannot read is -32602, per the specification's
// error handling for pagination: a cursor is the server's own token, so a malformed one means the
// client invented or corrupted it rather than echoing what it was given.
func decodeCursor(cursor string) (int, *JSONRPCError) {
	invalid := func() (int, *JSONRPCError) {
		return 0, &JSONRPCError{
			Code:    errCodeInvalidParams,
			Message: "Invalid cursor: not a cursor issued by this server. Re-request the list without a cursor to start over.",
		}
	}

	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return invalid()
	}
	text, ok := strings.CutPrefix(string(raw), cursorPrefix)
	if !ok {
		return invalid()
	}
	offset, err := strconv.Atoi(text)
	if err != nil || offset < 0 {
		return invalid()
	}
	return offset, nil
}

// paginate slices one page out of items and returns the cursor for the next one, or "" at the end.
//
// An offset past the end is an error rather than an empty final page. The only way to hold such a
// cursor is to have been given it, and we never issue one — so it means the list shrank underneath
// the client, and saying so lets it restart from the beginning instead of concluding, wrongly, that
// the wiki is empty.
func paginate[T any](items []T, cursor string, pageSize int) ([]T, string, *JSONRPCError) {
	offset := 0
	if cursor != "" {
		decoded, rpcErr := decodeCursor(cursor)
		if rpcErr != nil {
			return nil, "", rpcErr
		}
		offset = decoded
	}

	if offset > len(items) {
		return nil, "", &JSONRPCError{
			Code: errCodeInvalidParams,
			Message: fmt.Sprintf(
				"Cursor is past the end of the list (%d entries remain). The list changed since the cursor was issued; re-request without a cursor.",
				len(items)),
		}
	}

	end := min(offset+pageSize, len(items))
	page := items[offset:end]
	if end < len(items) {
		return page, encodeCursor(end), nil
	}
	return page, "", nil
}

// listResult assembles a paginated list payload, attaching nextCursor only when more pages exist.
// A nextCursor of "" MUST be omitted rather than emitted empty: clients are required to treat any
// non-null cursor — the empty string included — as "there is more", so sending one would loop them.
func listResult(key string, entries interface{}, nextCursor string) map[string]interface{} {
	result := map[string]interface{}{key: entries}
	if nextCursor != "" {
		result["nextCursor"] = nextCursor
	}
	return result
}
