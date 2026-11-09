import { Archive } from 'lucide-react';
import { useActions } from '../actions';
import type { Server } from '../api';
import { fmtBytes, fmtDate, Problem, Reload, Section, serverOptions, Table, Td } from '../common';
import { Button } from '../components/button';
import { useData } from '../hooks';

type Entry = { file: string; createdAt: string; size: number; sha256: string };

export function BackupsPage() {
  const { run, jobsChanged } = useActions();
  const backups = useData<Record<string, Entry[] | null>>('backups', jobsChanged);
  const servers = useData<Server[]>('servers', jobsChanged);
  const storage = useData<{ backupLocation: string }>('storage', jobsChanged);
  const location = storage.data?.backupLocation ?? '/var/backups/sharkord';

  const rows = Object.entries(backups.data ?? {})
    .flatMap(([id, list]) => (list ?? []).map((e) => ({ id, ...e })))
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt));

  return (
    <>
      <Section
        title="Backups"
        description={`Taken at 00:00 and 12:00, the last 14 kept per server, encrypted to the vault's key. Restoring needs the vault passphrase. They're in ${location}.`}
        actions={
          <>
            <Reload onClick={backups.reload} loading={backups.loading} />
            <Button
              onClick={() =>
                run({
                  action: 'server.backup',
                  title: 'Back up now',
                  fields: [{ name: 'id', label: 'Server', type: 'select', required: true, options: serverOptions(servers.data, true) }]
                })
              }
            >
              <Archive /> Back up now
            </Button>
          </>
        }
      >
        <Problem error={backups.error} />
        <Table
          head={['Server', 'Taken', 'Size', '']}
          empty={rows.length === 0 && !backups.loading && <p className="text-muted-foreground py-6 text-center text-sm">No backups yet.</p>}
        >
          {rows.map((r) => (
            <tr key={r.id + r.file}>
              <Td className="font-medium">{r.id}</Td>
              <Td>{fmtDate(r.createdAt)}</Td>
              <Td className="text-muted-foreground">{fmtBytes(r.size)}</Td>
              <Td className="text-right">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    run({
                      action: 'server.restore',
                      title: `Restore ${r.id}`,
                      destructive: true,
                      confirmLabel: 'Restore',
                      description: `Replaces ${r.id}'s data with this backup from ${fmtDate(r.createdAt)}. Everything since is lost; if the server doesn't start, the current data comes back.`,
                      params: { id: r.id, file: `${location}/${r.id}/${r.file}` },
                      fields: [{ name: 'passphrase', label: 'Vault passphrase', type: 'password', required: true }]
                    })
                  }
                >
                  Restore
                </Button>
              </Td>
            </tr>
          ))}
        </Table>
      </Section>
      <Section title="Copies elsewhere" description="Backups are encrypted, so they're safe to copy off the server.">
        <p className="text-sm">
          For example, with rclone: <code className="bg-muted rounded px-1 font-mono text-xs">rclone sync {location} remote:sharkord-backups</code>. Choose where
          backups go on the Storage page.
        </p>
      </Section>
    </>
  );
}
