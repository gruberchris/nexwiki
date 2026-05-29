/**
 * Slugify standardizes any title string into a clean, URL-safe, file-safe slug.
 * Matches the backend Go Slugify logic exactly.
 */
export function Slugify(title: string): string {
  if (!title) return '';
  
  let slug = title.toLowerCase();
  
  // Replace non-alphanumeric characters (excluding spaces, hyphens, and underscores) with nothing
  slug = slug.replace(/[^a-z0-9\s-_]/g, '');
  
  // Replace spaces and underscores with hyphens
  slug = slug.replace(/[\s_]+/g, '-');
  
  // Replace consecutive hyphens with a single hyphen
  slug = slug.replace(/-+/g, '-');
  
  // Trim leading and trailing hyphens
  return slug.replace(/^-+|-+$/g, '');
}
