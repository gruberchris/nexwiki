export default {
  plugins: {
    // Tailwind v4 ships its PostCSS integration as a separate package; the bare `tailwindcss`
    // entry from v3 is no longer a PostCSS plugin and throws if left here.
    '@tailwindcss/postcss': {},
    // autoprefixer is deliberately gone: v4 prefixes its own output, so a second pass would be
    // redundant work over already-prefixed CSS.
  },
}
