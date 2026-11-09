import { CircleAlert, Info, RefreshCw, TriangleAlert } from 'lucide-react';
import type { ReactNode } from 'react';
import type { Notice, Server } from './api';
import { Badge } from './components/badge';
import { Button } from './components/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './components/card';

export function Section({ title, description, actions, children }: { title: string; description?: ReactNode; actions?: ReactNode; children: ReactNode }) {
  return (
    <Card className="mb-6">
      <CardHeader className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="grid gap-1.5">
          <CardTitle>{title}</CardTitle>
          {description && <CardDescription>{description}</CardDescription>}
        </div>
        {actions && <div className="flex shrink-0 flex-wrap gap-2 sm:justify-end">{actions}</div>}
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  );
}

export function Reload({ onClick, loading }: { onClick: () => void; loading?: boolean }) {
  return (
    <Button variant="ghost" size="icon" title="Refresh" onClick={onClick}>
      <RefreshCw className={loading ? 'animate-spin' : ''} />
    </Button>
  );
}

export function Problem({ error }: { error: string | null }) {
  if (!error) return null;
  return <p className="text-muted-foreground text-sm">{error}</p>;
}

export function Notices({ notices }: { notices: Notice[] }) {
  if (notices.length === 0) return null;
  return (
    <div className="mb-6 grid gap-2">
      {notices.map((n) => {
        const Icon = n.level === 'error' ? CircleAlert : n.level === 'warning' ? TriangleAlert : Info;
        return (
          <div key={n.key} className="bg-card flex items-start gap-3 rounded-lg border px-4 py-3 text-sm">
            <Icon className={`mt-0.5 size-4 shrink-0 ${n.level === 'error' ? 'text-destructive' : 'text-muted-foreground'}`} />
            <span>{n.message}</span>
          </div>
        );
      })}
    </div>
  );
}

// Table is a plain, responsive table.
export function Table({ head, children, empty }: { head: string[]; children: ReactNode; empty?: ReactNode }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-sm">
        <thead className="text-muted-foreground border-b">
          <tr>
            {head.map((h) => (
              <th key={h} className="px-2 py-2 font-medium whitespace-nowrap">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="[&_tr]:border-b [&_tr:last-child]:border-0">{children}</tbody>
      </table>
      {empty}
    </div>
  );
}

export function Td({ children, className = '' }: { children?: ReactNode; className?: string }) {
  return <td className={`px-2 py-2 align-middle ${className}`}>{children}</td>;
}

// serverState is a server's state in a word, for badges.
export function serverState(s: Server): { label: string; variant: 'default' | 'secondary' | 'destructive' | 'outline' } {
  if (s.operation) return { label: `unfinished ${s.operation.name}`, variant: 'destructive' };
  if (s.locked) return { label: 'locked', variant: 'outline' };
  if (s.stopped) return { label: 'stopped', variant: 'secondary' };
  if (s.active === 'active' && s.healthy) return { label: 'running', variant: 'default' };
  if (s.active === 'active') return { label: 'not answering', variant: 'destructive' };
  return { label: s.active || 'down', variant: 'destructive' };
}

export function StateBadge({ s }: { s: Server }) {
  const st = serverState(s);
  return <Badge variant={st.variant}>{st.label}</Badge>;
}

export const serverOptions = (servers: Server[] | null, all = false) => [
  ...(all ? [{ value: '--all', label: 'Every server' }] : []),
  ...(servers ?? []).map((s) => ({ value: s.id, label: `${s.id} (${s.domain})` }))
];

export const fmtBytes = (b: number) => (b >= 1 << 30 ? `${(b / (1 << 30)).toFixed(1)} GiB` : `${(b / (1 << 20)).toFixed(1)} MiB`);
export const fmtDate = (s?: string) => (s ? new Date(s).toLocaleString() : '');
