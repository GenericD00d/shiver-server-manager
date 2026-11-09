import { Download, Ellipsis } from 'lucide-react';
import { useActions } from '../actions';
import type { Server } from '../api';
import { Problem, Reload, Section, Table, Td } from '../common';
import { Badge } from '../components/badge';
import { Button } from '../components/button';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '../components/dropdown-menu';
import { useData } from '../hooks';

type Cert = { instance: string; domain: string; issuer: string; present: boolean; daysLeft: number; notAfter?: string; pending: boolean; lastError?: string; warning?: string };
type Root = { sha256Fingerprint: string; notAfter: string; downloadURLs: string[] | null; warning?: string };

export function CertificatesPage() {
  const { run, jobsChanged } = useActions();
  const certs = useData<Cert[] | null>('certs', jobsChanged);
  const servers = useData<Server[]>('servers', jobsChanged);
  const root = useData<Root>('root', jobsChanged);
  const byId = Object.fromEntries((servers.data ?? []).map((s) => [s.id, s]));

  return (
    <>
      <Section
        title="Certificates"
        description="Caddy gets and renews them. Let's Encrypt allows 5 certificates a week per domain; use test mode while experimenting."
        actions={<Reload onClick={certs.reload} loading={certs.loading} />}
      >
        <Problem error={certs.error} />
        <Table head={['Server', 'Domain', 'Type', 'Status', '']}>
          {(certs.data ?? []).map((c) => {
            const s = byId[c.instance];
            return (
              <tr key={c.instance}>
                <Td className="font-medium">{c.instance}</Td>
                <Td>{c.domain}</Td>
                <Td className="text-muted-foreground">{s?.tls === 'selfsigned' ? 'self-signed' : s?.testMode ? 'staging (test mode)' : "Let's Encrypt"}</Td>
                <Td>
                  {c.pending ? (
                    <div>
                      <Badge variant="outline">pending</Badge>
                      {c.lastError && <p className="text-muted-foreground mt-1 text-xs">{c.lastError}</p>}
                    </div>
                  ) : (
                    <span>
                      {c.daysLeft} days left
                      {c.warning && <span className="text-destructive block text-xs">{c.warning}</span>}
                    </span>
                  )}
                </Td>
                <Td className="text-right">
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button variant="ghost" size="icon" title={`Certificate actions for ${c.instance}`}>
                        <Ellipsis />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      <DropdownMenuItem
                        onSelect={() =>
                          run({ action: 'cert.retry', title: `Retry ${c.domain}`, description: 'Rechecks DNS, then has Caddy try again at once.', params: { id: c.instance } })
                        }
                      >
                        Retry now
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        onSelect={() =>
                          run({
                            action: 'cert.switch',
                            title: `Certificate type for ${c.instance}`,
                            description: 'Self-signed members install the root certificate below. The manager never falls back to self-signed by itself.',
                            params: { id: c.instance },
                            fields: [
                              {
                                name: 'tls',
                                label: 'Type',
                                type: 'select',
                                required: true,
                                options: [
                                  { value: 'letsencrypt', label: "Let's Encrypt" },
                                  { value: 'selfsigned', label: 'Self-signed' }
                                ]
                              },
                              { name: 'testMode', label: "Test mode (Let's Encrypt's staging server)", type: 'switch' }
                            ]
                          })
                        }
                      >
                        Switch type or test mode
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </Td>
              </tr>
            );
          })}
        </Table>
      </Section>

      <Section
        title="Self-signed root certificate"
        description="Members of self-signed servers install this once per device. The download isn't encrypted, so send them the fingerprint another way (chat, email) to compare."
        actions={
          root.data && (
            <Button asChild variant="outline">
              <a href="/api/root.crt" download="sharkord-root.crt">
                <Download /> Download
              </a>
            </Button>
          )
        }
      >
        {root.error ? (
          <p className="text-muted-foreground text-sm">There's no self-signed root yet; Caddy makes it with the first self-signed server.</p>
        ) : (
          root.data && (
            <div className="grid gap-3 text-sm">
              <div>
                <div className="text-muted-foreground text-xs">SHA-256 fingerprint</div>
                <code className="font-mono text-xs break-all select-all">{root.data.sha256Fingerprint}</code>
              </div>
              <div>
                <div className="text-muted-foreground text-xs">Expires</div>
                {new Date(root.data.notAfter).toLocaleDateString()}
              </div>
              {root.data.warning && <p className="text-destructive">{root.data.warning}</p>}
              {(root.data.downloadURLs ?? []).length > 0 && (
                <div>
                  <div className="text-muted-foreground text-xs">Members download it from</div>
                  {root.data.downloadURLs!.map((u) => (
                    <code key={u} className="block font-mono text-xs">
                      {u}
                    </code>
                  ))}
                </div>
              )}
            </div>
          )
        )}
      </Section>
    </>
  );
}
