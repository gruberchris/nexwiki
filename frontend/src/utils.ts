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

/**
 * Saves a text string as a local file, prompting the user with a native
 * OS Save As file picker if supported, or falling back to a standard browser download.
 */
export async function saveFile(
  content: string,
  suggestedName: string,
  mimeType: string,
  extension: string
): Promise<boolean> {
  // Try modern File System Access API
  if ('showSaveFilePicker' in window) {
    try {
      const handle = await (window as unknown as {
        showSaveFilePicker: (options: unknown) => Promise<{
          createWritable: () => Promise<{
            write: (data: unknown) => Promise<void>;
            close: () => Promise<void>;
          }>;
        }>;
      }).showSaveFilePicker({
        suggestedName: `${suggestedName}.${extension}`,
        types: [
          {
            description: extension === 'docx' ? 'Word Document (.docx)' : 'Document File',
            accept: {
              [mimeType]: [`.${extension}`]
            }
          }
        ]
      });
      const writable = await handle.createWritable();
      await writable.write(content);
      await writable.close();
      return true;
    } catch (err: unknown) {
      if (err && typeof err === 'object' && 'name' in err && (err as Record<string, unknown>).name === 'AbortError') {
        return false; // User canceled dialog
      }
      console.warn('File System Access API failed or cancelled, using standard fallback:', err);
    }
  }

  // Fallback: Standard browser download trigger
  const blob = new Blob([content], { type: mimeType });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `${suggestedName}.${extension}`;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
  return true;
}

/**
 * Wraps rendered HTML in Office XML namespace markup.
 * Enables MS Word and Apple Pages to open the HTML document as an editable DOCX file.
 */
export function generateDocxContent(title: string, bodyHtml: string): string {
  const attrs = [
    'xmlns:o="urn:schemas-microsoft-com:office:office"',
    'xmlns:w="urn:schemas-microsoft-com:office:word"',
    'xmlns="http://www.w3.org/TR/REC-html40"'
  ].join(' ');
  const htmlOpen = '<html lang="en" ' + attrs + '>';
  return `
${htmlOpen}
<head>
  <meta charset="utf-8">
  <title>${title}</title>
  <!--[if gte mso 9]>
  <xml>
    <w:WordDocument>
      <w:View>Print</w:View>
      <w:Zoom>100</w:Zoom>
      <w:DoNotOptimizeForBrowser/>
    </w:WordDocument>
  </xml>
  <![endif]-->
  <style>
    body {
      font-family: 'Calibri', 'Segoe UI', Arial, sans-serif;
      font-size: 11pt;
      line-height: 1.5;
      color: #333333;
      margin: 1in;
    }
    h1 {
      font-size: 24pt;
      font-weight: bold;
      color: #1e3a8a;
      margin-top: 12pt;
      margin-bottom: 6pt;
      border-bottom: 1px solid #cbd5e1;
      padding-bottom: 3pt;
    }
    h2 {
      font-size: 18pt;
      font-weight: bold;
      color: #0f172a;
      margin-top: 18pt;
      margin-bottom: 6pt;
      border-bottom: 1px solid #e2e8f0;
      padding-bottom: 2pt;
    }
    h3 {
      font-size: 14pt;
      font-weight: bold;
      color: #334155;
      margin-top: 14pt;
      margin-bottom: 4pt;
    }
    p {
      margin-bottom: 8pt;
      color: #334155;
    }
    a {
      color: #4f46e5;
      text-decoration: underline;
    }
    ul {
      list-style-type: disc;
      margin-left: 20pt;
      margin-bottom: 8pt;
    }
    ol {
      list-style-type: decimal;
      margin-left: 20pt;
      margin-bottom: 8pt;
    }
    blockquote {
      border-left: 3pt solid #4f46e5;
      background-color: #f8fafc;
      padding: 10pt 12pt;
      margin: 12pt 0;
      font-style: italic;
      color: #475569;
    }
    pre {
      background-color: #0f172a;
      color: #f8fafc;
      padding: 10pt;
      font-family: Consolas, Courier, monospace;
      font-size: 9.5pt;
      margin: 12pt 0;
    }
    code {
      font-family: Consolas, Courier, monospace;
      font-size: 9.5pt;
      background-color: #f1f5f9;
      color: #b45309;
      padding: 1px 3px;
    }
    pre code {
      background-color: transparent;
      color: inherit;
      padding: 0;
    }
    table {
      border-collapse: collapse;
      width: 100%;
      margin: 12pt 0;
    }
    th {
      background-color: #f8fafc;
      border: 1px solid #e2e8f0;
      padding: 6pt 8pt;
      text-align: left;
      font-weight: bold;
      color: #0f172a;
    }
    td {
      border: 1px solid #e2e8f0;
      padding: 6pt 8pt;
      color: #334155;
    }
    img {
      max-width: 100%;
      height: auto;
      margin: 12pt 0;
    }
  </style>
</head>
<body>
  ${bodyHtml}
</body>
</html>`;
}

