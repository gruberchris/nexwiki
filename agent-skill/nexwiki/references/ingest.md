# Ingest: compile an external source into the wiki

For a URL, article, transcript, or pile of notes — handle **one source per pass**.

1. **Read the source fully** before writing anything. For a URL, fetch it with your own
   web-fetch capability — NexWiki does not fetch URLs. If you cannot reach it, ask the user
   to paste the content.
2. `get_context_overview` so you don't duplicate an existing page.
3. Synthesize **one** wiki article in the wiki's voice (a compilation, not a transcript)
   with `create_wiki_article`, setting `description` and `source` (the citation), and
   cross-link related pages with `[[WikiLinks]]`.
4. **Flag contradictions** with existing content for the user's review — never silently
   overwrite.
5. If the source came from an `inbox`-tagged dump, remove the `inbox` tag from it (or
   delete the dump) once compiled.
6. Report what you created, linked, and flagged, then **stop** so the user can steer.
