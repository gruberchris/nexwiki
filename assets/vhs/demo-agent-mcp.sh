#!/usr/bin/env bash
# Demo simulation: AI agent MCP ingestion workflow into NexWiki
set -e

# Terminal colors and styling (ANSI)
ESC=$'\033'
RESET="${ESC}[0m"
BOLD="${ESC}[1m"
DIM="${ESC}[2m"
ITALIC="${ESC}[3m"

# Catppuccin Mocha-inspired ANSI palette
CYAN="${ESC}[38;2;137;220;235m"
BLUE="${ESC}[38;2;137;180;250m"
LAVENDER="${ESC}[38;2;180;190;254m"
GREEN="${ESC}[38;2;166;227;161m"
YELLOW="${ESC}[38;2;249;226;175m"
PEACH="${ESC}[38;2;250;179;135m"
GRAY="${ESC}[38;2;108;112;134m"
WHITE="${ESC}[38;2;205;214;244m"

# Typewriter effect for natural typing simulation
typewrite() {
    local text="$1"
    local delay="${2:-0.02}"
    for ((i=0; i<${#text}; i++)); do
        printf "%s" "${text:$i:1}"
        sleep "$delay"
    done
    printf "\n"
}

# Spinner animation helper
spinner() {
    local label="$1"
    local frames=('⠋' '⠙' '⠹' '⠸' '⠼' '⠴' '⠦' '⠧' '⠇' '⠏')
    local spin_count="${2:-12}"
    for ((k=0; k<spin_count; k++)); do
        local frame="${frames[$((k % 10))]}"
        printf "\r  ${LAVENDER}%s${RESET} ${DIM}%s${RESET}" "$frame" "$label"
        sleep 0.08
    done
    printf "\r\033[K"
}

# 1. Header: Agent CLI startup
printf "  ${BOLD}${BLUE}Claude Code${RESET} ${DIM}v0.2.29${RESET} ${GRAY}•${RESET} ${WHITE}personal-wiki${RESET}\n"
printf "  ${GREEN}●${RESET} ${DIM}Connected to${RESET} ${BOLD}${CYAN}nexwiki${RESET} ${DIM}MCP server (Streamable HTTP • 31 tools active)${RESET}\n\n"
sleep 0.6

# 2. User prompt
printf "  ${BOLD}${BLUE}❯${RESET} "
prompt_text="Ingest this Slack decision thread into my NexWiki under tag 'architecture': 'Team agreed to use token bucket rate limiting with Redis backend, 100 req/min per tenant. Owner: @sarah'"
typewrite "${WHITE}${prompt_text}${RESET}" 0.018
sleep 0.5

# 3. Agent thinking & MCP tool call: create_wiki_article
spinner "Agent parsing Slack snippet & formatting OKF article..." 14

printf "  ${CYAN}╭─${RESET} ${BOLD}MCP Tool Call: ${CYAN}nexwiki.create_wiki_article${RESET} ${CYAN}───────────────────────────────────╮${RESET}\n"
printf "  ${CYAN}│${RESET}  ${BOLD}title:${RESET}   ${WHITE}\"API Rate Limiting Architecture Decision\"${RESET}                  ${CYAN}│${RESET}\n"
printf "  ${CYAN}│${RESET}  ${BOLD}tags:${RESET}    ${YELLOW}[\"architecture\", \"rate-limiting\", \"redis\"]${RESET}                 ${CYAN}│${RESET}\n"
printf "  ${CYAN}│${RESET}  ${BOLD}source:${RESET}  ${DIM}\"Slack #architecture-decisions (@sarah)\"${RESET}                   ${CYAN}│${RESET}\n"
printf "  ${CYAN}│${RESET}  ${BOLD}type:${RESET}    ${PEACH}\"Wiki\" (OKF v0.2 front matter)${RESET}                           ${CYAN}│${RESET}\n"
printf "  ${CYAN}╰────────────────────────────────────────────────────────────────────────╯${RESET}\n"

spinner "Invoking create_wiki_article & writing to storage..." 15

printf "  ${GREEN}✔${RESET} ${BOLD}Article created:${RESET} ${CYAN}/articles/api-rate-limiting-architecture-decision${RESET}\n"
printf "    ${DIM}Indexed in Bleve search index • 194 words • version 1 • 0 broken links${RESET}\n"
printf "\n"
sleep 0.8

# 4. Agent verification: search_wiki query
spinner "Verifying context grounding via search_wiki..." 12

printf "  ${CYAN}╭─${RESET} ${BOLD}MCP Tool Call: ${CYAN}nexwiki.search_wiki${RESET} ───────────────────────────────────────────${CYAN}╮${RESET}\n"
printf "  ${CYAN}│${RESET}  ${BOLD}query:${RESET}   ${WHITE}\"rate limiting algorithm\"${RESET}                                        ${CYAN}│${RESET}\n"
printf "  ${CYAN}╰────────────────────────────────────────────────────────────────────────╯${RESET}\n"

spinner "Querying Bleve full-text engine..." 14

printf "  ${GREEN}✔${RESET} ${BOLD}1 match found${RESET} ${DIM}(Bleve score: 0.942)${RESET}:\n"
printf "    ${BOLD}•${RESET} ${WHITE}API Rate Limiting Architecture Decision${RESET}\n"
printf "      ${DIM}(/articles/api-rate-limiting-architecture-decision)${RESET}\n"
printf "      ${ITALIC}${YELLOW}\"...agreed to use token bucket rate limiting with Redis backend, 100 req/min...\"${RESET}\n"
printf "\n"
sleep 0.6

# 5. Conclusion: Grounded context ready
printf "  ${BOLD}${GREEN}✔ Grounded Context Ready${RESET}\n"
printf "  ${WHITE}Slack decision persisted to NexWiki and indexed for instant AI retrieval.${RESET}\n"
printf "\n"
sleep 2.0
