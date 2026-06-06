import { describe, it, expect, vi, afterEach } from 'vitest';
import { Slugify, formatRelativeTime, generateDocxContent } from './utils';

describe('Slugify', () => {
  it('returns empty string for empty/falsy input', () => {
    expect(Slugify('')).toBe('');
  });

  it('converts to lowercase', () => {
    expect(Slugify('Hello World')).toBe('hello-world');
  });

  it('replaces spaces with hyphens', () => {
    expect(Slugify('my article title')).toBe('my-article-title');
  });

  it('strips special characters', () => {
    expect(Slugify('Hello, World!')).toBe('hello-world');
  });

  it('replaces underscores with hyphens', () => {
    expect(Slugify('hello_world')).toBe('hello-world');
  });

  it('collapses consecutive hyphens', () => {
    expect(Slugify('hello--world')).toBe('hello-world');
    expect(Slugify('hello   world')).toBe('hello-world');
  });

  it('trims leading and trailing hyphens', () => {
    expect(Slugify('  Hello World  ')).toBe('hello-world');
    expect(Slugify('-hello-')).toBe('hello');
  });

  it('handles numbers', () => {
    expect(Slugify('Go 1.22 Release')).toBe('go-122-release');
  });

  it('handles all special chars stripped', () => {
    expect(Slugify('!@#$%^&*()')).toBe('');
  });
});

describe('formatRelativeTime', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  function timeAgo(seconds: number): string {
    const d = new Date(Date.now() - seconds * 1000);
    return d.toISOString();
  }

  it('returns "Just now" for very recent times', () => {
    expect(formatRelativeTime(timeAgo(5))).toBe('Just now');
  });

  it('returns seconds ago', () => {
    expect(formatRelativeTime(timeAgo(30))).toBe('30s ago');
  });

  it('returns minutes ago', () => {
    expect(formatRelativeTime(timeAgo(5 * 60))).toBe('5m ago');
  });

  it('returns hours ago', () => {
    expect(formatRelativeTime(timeAgo(3 * 3600))).toBe('3h ago');
  });

  it('returns Yesterday for 1 day ago', () => {
    expect(formatRelativeTime(timeAgo(25 * 3600))).toBe('Yesterday');
  });

  it('returns days ago for 2-6 days', () => {
    expect(formatRelativeTime(timeAgo(3 * 24 * 3600))).toBe('3d ago');
  });

  it('returns formatted date for 7+ days ago', () => {
    const result = formatRelativeTime(timeAgo(10 * 24 * 3600));
    // Should contain a month abbreviation
    expect(result).toMatch(/[A-Za-z]+/);
    expect(result).not.toContain('ago');
  });
});

describe('generateDocxContent', () => {
  it('returns an HTML string', () => {
    const result = generateDocxContent('My Title', '<p>Body content</p>');
    expect(result).toContain('<html');
    expect(result).toContain('</html>');
  });

  it('includes the title in the <title> tag', () => {
    const result = generateDocxContent('Test Article', '<p>content</p>');
    expect(result).toContain('<title>Test Article</title>');
  });

  it('includes the body HTML', () => {
    const result = generateDocxContent('Title', '<p>Body paragraph</p>');
    expect(result).toContain('<p>Body paragraph</p>');
  });

  it('includes Office XML namespaces', () => {
    const result = generateDocxContent('Title', 'Body');
    expect(result).toContain('xmlns:w="urn:schemas-microsoft-com:office:word"');
    expect(result).toContain('xmlns:o="urn:schemas-microsoft-com:office:office"');
  });

  it('includes lang="en" on html element', () => {
    const result = generateDocxContent('Title', 'Body');
    expect(result).toContain('lang="en"');
  });
});
