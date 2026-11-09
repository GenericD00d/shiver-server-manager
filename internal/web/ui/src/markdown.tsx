import type { ReactNode } from 'react';

// inline renders `code`, **bold** and bare links in one line of text.
function inline(text: string): ReactNode[] {
  const out: ReactNode[] = [];
  const re = /(`[^`]+`|\*\*[^*]+\*\*|https?:\/\/[^\s)]+)/g;
  let last = 0;
  for (const m of text.matchAll(re)) {
    if (m.index! > last) out.push(text.slice(last, m.index));
    const t = m[0];
    if (t.startsWith('`')) out.push(<code key={m.index} className="bg-muted rounded px-1 font-mono text-xs">{t.slice(1, -1)}</code>);
    else if (t.startsWith('**')) out.push(<b key={m.index}>{t.slice(2, -2)}</b>);
    else
      out.push(
        <a key={m.index} href={t} target="_blank" rel="noreferrer" className="underline underline-offset-4">
          {t}
        </a>
      );
    last = m.index! + t.length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

// Markdown renders the small subset the help text uses: headings,
// paragraphs, bullet lists and indented code. It never renders HTML.
export function Markdown({ text }: { text: string }) {
  const blocks = text.replace(/\r/g, '').split(/\n{2,}/);
  return (
    <div className="grid gap-3 text-sm leading-relaxed">
      {blocks.map((b, i) => {
        if (b.startsWith('# ')) return null; // the page shows its own title
        if (b.startsWith('## ')) return <h3 key={i} className="mt-3 text-base font-semibold">{b.slice(3)}</h3>;
        const lines = b.split('\n');
        if (lines.every((l) => l.startsWith('    '))) {
          return (
            <pre key={i} className="bg-muted overflow-x-auto rounded-md p-3 font-mono text-xs">
              {lines.map((l) => l.slice(4)).join('\n')}
            </pre>
          );
        }
        if (lines.every((l) => l.startsWith('- '))) {
          return (
            <ul key={i} className="ml-5 list-disc space-y-1">
              {lines.map((l, j) => (
                <li key={j}>{inline(l.slice(2))}</li>
              ))}
            </ul>
          );
        }
        return <p key={i}>{inline(lines.join(' '))}</p>;
      })}
    </div>
  );
}
