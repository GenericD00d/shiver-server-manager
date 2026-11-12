import { CircleAlert, CircleCheck, Ellipsis, Eye, Import, Info, Plus } from 'lucide-react';
import { useState } from 'react';
import type { PageProps } from '../App';
import { useActions, type ActionSpec, type Field } from '../actions';
import { api, ApiError, type CatalogPlugin, type Health, type Notice, type PluginList, type Server } from '../api';
import { fmtDate, Problem, Reload, Section, StateBadge, Table, Td, Notices } from '../common';
import { Button } from '../components/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '../components/dialog';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger
} from '../components/dropdown-menu';
import { Input } from '../components/input';
import { Label } from '../components/label';
import { useData } from '../hooks';

const tlsOptions = [
  { value: 'letsencrypt', label: "Let's Encrypt (trusted everywhere; needs ports 80 and 443 open)" },
  { value: 'selfsigned', label: 'Self-signed (members install a root certificate)' }
];

export function ServersPage({ meta }: PageProps) {
  const { run, jobsChanged } = useActions();
  const servers = useData<Server[]>('servers', jobsChanged);
  const health = useData<Health>('health', jobsChanged);
  const pluginLists = useData<PluginList[] | null>('plugins', jobsChanged);
  const catalog = useData<CatalogPlugin[]>('catalog', jobsChanged);
  const storage = useData<{ locations: { path: string; default?: boolean }[] }>('storage', jobsChanged);
  const locations = storage.data?.locations ?? [];
  const defaultLocation = locations.find((l) => l.default)?.path ?? '';
  const locationField: Field = {
    name: 'location',
    label: 'Storage location',
    type: 'select',
    initial: defaultLocation,
    options: locations.map((l) => ({ value: l.path, label: l.default ? `${l.path} (default)` : l.path })),
    help: 'Where its data lives. Change the default, or add folders on other drives, on the Storage page.'
  };
  const skipDNS: Field = {
    name: 'skipDnsCheck',
    label: 'The domain points at a proxy in front of this server',
    type: 'switch',
    help: "Skips the check that the domain's A record holds this server's address. Self-signed servers never check."
  };
  const [tokenFor, setTokenFor] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);

  const createSpec: ActionSpec = {
    action: 'server.create',
    title: 'Create a server',
    description: "Point the domain's A record at this server first. Everything else has a default.",
    fields: [
      { name: 'id', label: 'Server ID', required: true, help: 'Lowercase letters, digits and hyphens, starting with a letter; up to 20.' },
      { name: 'domain', label: 'Domain', required: true, placeholder: 'chat.example.com' },
      { name: 'tls', label: 'Certificate', type: 'select', options: tlsOptions, initial: 'letsencrypt', required: true },
      ...(locations.length > 1 ? [locationField] : []),
      {
        name: 'encrypt',
        label: 'Encrypted data folder',
        type: 'switch',
        initial: 'true',
        help: 'Protects a stolen disk; after a reboot, the server waits for the vault to be unlocked.'
      },
      skipDNS
    ]
  };
  const importSpec: ActionSpec = {
    action: 'server.import',
    title: 'Import a server',
    description: 'From an export or backup file, a Sharkord data folder, or a Docker container. A server still running must be stopped for the final copy.',
    fields: [
      { name: 'source', label: 'Import from', required: true, placeholder: '/root/chat.tar.zst.age, /home/me/.config/sharkord or docker:sharkord' },
      { name: 'id', label: 'New server ID', required: true },
      { name: 'domain', label: 'Domain', required: true },
      {
        name: 'tls',
        label: 'Certificate',
        type: 'select',
        initial: 'same',
        options: [{ value: 'same', label: "The same as the export (or Let's Encrypt)" }, ...tlsOptions]
      },
      ...(locations.length > 1 ? [locationField] : []),
      { name: 'passphrase', label: "Export's passphrase (or the vault's, for a backup)", type: 'password' },
      { name: 'token', label: "The old server's secret token (optional)", type: 'password', help: 'An export with the token inside doesn’t need it.' },
      { name: 'encrypt', label: 'Encrypted data folder', type: 'switch', initial: 'true' },
      skipDNS
    ]
  };

  const list = servers.data ?? [];
  const notices = health.data?.notices ?? [];
  const updates = (id: string) =>
    (catalog.data ?? []).filter((p) => p.servers.some((x) => x.instance === id && x.update)).map((p) => ({ name: p.name, latest: p.latest ?? '' }));
  const current = list.find((s) => s.id === selected) ?? list[0];
  return (
    <>
      <Notices notices={notices.filter((n) => n.level !== 'info' || n.key.startsWith('locked'))} />
      <Section
        title="Your servers"
        description="Each is a Sharkord community on its own domain. Select one to see its details."
        actions={
          <>
            <Reload onClick={servers.reload} loading={servers.loading} />
            <Button variant="outline" onClick={() => run(importSpec)}>
              <Import /> Import
            </Button>
            <Button onClick={() => run(createSpec)}>
              <Plus /> Create
            </Button>
          </>
        }
      >
        <Problem error={servers.error} />
        <Table
          head={['Server', 'State', 'Domain', 'Certificate', 'Sharkord', 'Storage', '']}
          empty={list.length === 0 && !servers.loading && !servers.error && <p className="text-muted-foreground py-6 text-center text-sm">No servers yet.</p>}
        >
          {list.map((s) => {
            const isCurrent = current?.id === s.id;
            const ups = updates(s.id);
            return (
              <tr
                key={s.id}
                tabIndex={0}
                aria-selected={isCurrent}
                onClick={() => setSelected(s.id)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    setSelected(s.id);
                  }
                }}
                className={`hover:bg-muted/50 focus-visible:ring-ring cursor-pointer outline-none focus-visible:ring-2 ${isCurrent ? 'bg-muted' : ''}`}
              >
                <Td className="font-medium">
                  <span className="inline-flex items-center gap-1.5">
                    <HealthIcon s={s} notices={notices.filter((n) => n.instance === s.id)} />
                    {s.id}
                    {ups.length > 0 && (
                      <span title={`Plugin update available: ${ups.map((u) => `${u.name} ${u.latest}`).join(', ')}`} aria-label="Plugin update available">
                        <Info className="text-primary size-4" />
                      </span>
                    )}
                  </span>
                </Td>
                <Td>
                  <StateBadge s={s} />
                </Td>
                <Td>
                  <a
                    className="underline-offset-4 hover:underline"
                    href={`https://${s.domain}`}
                    target="_blank"
                    rel="noreferrer"
                    onClick={(e) => e.stopPropagation()}
                  >
                    {s.domain}
                  </a>
                </Td>
                <Td className="text-muted-foreground">{s.tls === 'selfsigned' ? 'self-signed' : s.testMode ? 'staging' : "Let's Encrypt"}</Td>
                <Td className="text-muted-foreground">{s.sharkordVersion}</Td>
                <Td className="text-muted-foreground">
                  {s.location}
                  {s.encrypted && ' · encrypted'}
                </Td>
                <Td className="text-right">
                  <span onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
                    <ServerMenu s={s} meta={meta} onShowToken={() => setTokenFor(s.id)} />
                  </span>
                </Td>
              </tr>
            );
          })}
        </Table>
      </Section>
      {current && (
        <ServerDetails
          s={current}
          notices={notices.filter((n) => n.instance === current.id)}
          checkedAt={health.data?.checkedAt}
          plugins={(pluginLists.data ?? []).find((l) => l.instance === current.id)?.plugins ?? null}
          pluginsError={pluginLists.error}
          catalog={catalog.data}
        />
      )}
      {tokenFor && <TokenDialog id={tokenFor} onClose={() => setTokenFor(null)} />}
    </>
  );
}

function ServerMenu({ s, meta, onShowToken }: { s: Server; meta: PageProps['meta']; onShowToken: () => void }) {
  const { run } = useActions();
  const id = { id: s.id };
  const item = (label: string, spec: Omit<ActionSpec, 'params'> & { params?: Record<string, string> }, destructive = false) => (
    <DropdownMenuItem variant={destructive ? 'destructive' : 'default'} onSelect={() => run({ ...spec, params: { ...id, ...spec.params } })}>
      {label}
    </DropdownMenuItem>
  );
  const passphrase = { name: 'passphrase', label: 'Vault passphrase', type: 'password' as const, required: true };
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" title={`Actions for ${s.id}`}>
          <Ellipsis />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuLabel>{s.id}</DropdownMenuLabel>
        {s.locked
          ? item('Unlock (vault)', {
              action: 'vault.unlock',
              title: 'Unlock the vault',
              description: 'Opens every encrypted server waiting since a reboot, and starts it.',
              fields: [passphrase]
            })
          : s.active === 'active'
            ? item('Stop', {
                action: 'server.stop',
                title: `Stop ${s.id}`,
                description: 'It stays stopped until you start it; repair and the health check leave it alone.'
              })
            : item('Start', { action: 'server.start', title: `Start ${s.id}` })}
        {item('Restart', { action: 'server.restart', title: `Restart ${s.id}` })}
        {item('Change a setting', {
          action: 'server.set',
          title: `Change a setting on ${s.id}`,
          description: 'Sharkord’s settings; the server restarts. Leave the value empty to go back to the default.',
          fields: [
            {
              name: 'key',
              label: 'Setting',
              type: 'select',
              required: true,
              options: meta.settings.map((x) => ({ value: x.Name, label: `${x.Name} — ${x.Help}` }))
            },
            { name: 'value', label: 'Value', help: 'Lists are comma-separated.' }
          ]
        })}
        {item('Limit memory and CPU', {
          action: 'server.limits',
          title: `Limit ${s.id}`,
          fields: [
            { name: 'memory', label: 'Memory cap (MiB, 0 = none)', type: 'number', initial: String(s.memoryMaxMiB ?? 0) },
            { name: 'cpu', label: 'CPU cap (% of one core, 0 = none)', type: 'number', initial: String(s.cpuPercent ?? 0) }
          ]
        })}
        {item('Change domain', {
          action: 'server.domain',
          title: `Move ${s.id} to a new domain`,
          description: 'The new domain gets its certificate first, then the old one is retired. Members need the new address.',
          fields: [{ name: 'domain', label: 'New domain', required: true }]
        })}
        {item('Update Sharkord', {
          action: 'server.update',
          title: `Update ${s.id}`,
          description: 'Backs up, switches to the newest tested version, and rolls back if it doesn’t start.',
          fields: [passphrase]
        })}
        <DropdownMenuSeparator />
        {item('Back up now', { action: 'server.backup', title: `Back up ${s.id}` })}
        {item('Export', {
          action: 'server.export',
          title: `Export ${s.id}`,
          description: 'An encrypted file with its own passphrase, to move the server to another machine.',
          fields: [
            { name: 'out', label: 'Save to', required: true, initial: `/var/backups/sharkord/${s.id}-export.tar.zst.age` },
            { name: 'exportPassphrase', label: 'Passphrase for the export (12+ characters)', type: 'password', required: true },
            { name: 'includeToken', label: 'Include the secret token', type: 'switch' },
            { name: 'passphrase', label: 'Vault passphrase (only to include the token)', type: 'password' }
          ]
        })}
        {item('Move to other storage', {
          action: 'server.move',
          title: `Move ${s.id}`,
          description: 'It’s offline while its data is copied; the old copy is kept until you delete it.',
          fields: [{ name: 'to', label: 'Storage location', required: true, placeholder: '/srv/sharkord-disk2/sharkord' }, ...(s.encrypted ? [passphrase] : [])]
        })}
        {s.encrypted &&
          item('Grow storage', {
            action: 'server.grow',
            title: `Grow ${s.id}'s container`,
            fields: [{ name: 'size', label: 'New size (GiB)', type: 'number', required: true }, passphrase]
          })}
        <DropdownMenuSeparator />
        {item('Install a plugin from a file', {
          action: 'plugin.install',
          title: `Install a plugin on ${s.id}`,
          confirmLabel: 'Install',
          description:
            'A Sharkord plugin as a .zip or .tar (.tar.gz, .tar.zst): manifest.json, server/index.js and client/index.js. The server is backed up and restarted; if Sharkord can’t load the plugin, the previous copy comes back. Only install plugins you trust: they run inside Sharkord with access to its data.',
          fields: [{ name: 'file', label: 'Plugin file (up to 100 MB)', type: 'file', accept: '.zip,.tar,.gz,.tgz,.zst,.tzst', required: true }]
        })}
        <DropdownMenuItem onSelect={onShowToken}>
          <Eye /> Show secret token
        </DropdownMenuItem>
        {item('Save secret token', {
          action: 'token.save',
          title: `Save ${s.id}'s secret token`,
          description: 'Checked against the server’s database first.',
          fields: [{ name: 'token', label: 'Secret token', type: 'password', required: true }]
        })}
        {item(
          'New secret token…',
          {
            action: 'token.new',
            title: `New secret token for ${s.id}`,
            destructive: true,
            confirmLabel: 'Make a new token',
            description:
              'Makes a new secret (owner) token and saves it in the vault; the old one stops working. Sharkord also signs logins with it, so everyone on the server has to log in again. The server is backed up and restarted. Afterwards, see the new token with Show secret token.'
          },
          true
        )}
        <DropdownMenuSeparator />
        {item(
          'Delete…',
          {
            action: 'server.delete',
            title: `Delete ${s.id}`,
            destructive: true,
            confirmLabel: 'Delete for good',
            description: `This erases ${s.domain}'s community for good: messages, uploads, accounts and settings. Export it first if you might want it back.`,
            fields: [{ name: 'confirmId', label: `Type ${s.id} to confirm`, required: true }]
          },
          true
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// isUnhealthy: something an admin should look at, as opposed to a server
// stopped on purpose or waiting for the vault.
const isUnhealthy = (s: Server, notices: Notice[]) =>
  !!s.operation || notices.some((n) => n.level !== 'info') || (!s.stopped && !s.locked && (s.active !== 'active' || !s.healthy));

// HealthIcon is a tick for a healthy server and a warning for one with
// problems; nothing while it's stopped or locked on purpose.
function HealthIcon({ s, notices }: { s: Server; notices: Notice[] }) {
  if (isUnhealthy(s, notices)) {
    const why = s.operation ? `unfinished ${s.operation.name}` : (notices.find((n) => n.level !== 'info')?.message ?? 'not answering');
    return (
      <span title={`Needs attention: ${why}`} aria-label="Needs attention">
        <CircleAlert className="text-destructive size-4" />
      </span>
    );
  }
  if (s.stopped || s.locked) return <span className="inline-block size-4" />;
  return (
    <span title="Healthy" aria-label="Healthy">
      <CircleCheck className="size-4 text-emerald-600 dark:text-emerald-500" />
    </span>
  );
}

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-0.5">
      <dt className="text-muted-foreground text-xs">{label}</dt>
      <dd className="text-sm break-words">{children}</dd>
    </div>
  );
}

// ServerDetails is the selected server's settings, health and plugins.
function ServerDetails({
  s,
  notices,
  checkedAt,
  plugins,
  pluginsError,
  catalog
}: {
  s: Server;
  notices: Notice[];
  checkedAt?: string;
  plugins: PluginList['plugins'] | null;
  pluginsError: string | null;
  catalog: CatalogPlugin[] | null;
}) {
  const problems = notices.filter((n) => n.level !== 'info');
  const latest = (id: string) => {
    const c = catalog?.find((p) => p.id === id);
    const mine = c?.servers.find((x) => x.instance === s.id);
    return mine?.update ? c?.latest : undefined;
  };
  const changed = Object.entries(s.settings ?? {});
  return (
    <Section
      title={s.id}
      description={
        <a className="underline-offset-4 hover:underline" href={`https://${s.domain}`} target="_blank" rel="noreferrer">
          {s.domain}
        </a>
      }
      actions={<StateBadge s={s} />}
    >
      <div className="grid gap-6">
        <dl className="grid grid-cols-2 gap-x-6 gap-y-3 sm:grid-cols-3 lg:grid-cols-4">
          <Fact label="Sharkord">{s.sharkordVersion}</Fact>
          <Fact label="Certificate">{s.tls === 'selfsigned' ? 'Self-signed' : s.testMode ? "Let's Encrypt (staging)" : "Let's Encrypt"}</Fact>
          <Fact label="Web port (local)">127.0.0.1:{s.httpPort}</Fact>
          <Fact label="Voice port">{s.webrtcPort} TCP and UDP</Fact>
          <Fact label="Data folder">
            <span className="font-mono text-xs">{s.dataPath}</span>
          </Fact>
          <Fact label="Encryption">{s.encrypted ? (s.locked ? 'Encrypted, waiting for the vault' : 'Encrypted') : 'Not encrypted'}</Fact>
          <Fact label="Limits">
            {s.memoryMaxMiB ? `${s.memoryMaxMiB} MiB` : 'no memory cap'}, {s.cpuPercent ? `${s.cpuPercent}% CPU` : 'no CPU cap'}
          </Fact>
          <Fact label="Secret token">{s.tokenSaved ? 'Saved in the vault' : 'Not saved'}</Fact>
          {s.skipDNSCheck && <Fact label="DNS check">Skipped (behind a proxy)</Fact>}
        </dl>

        <div className="grid gap-2">
          <h3 className="text-sm font-medium">Health</h3>
          {s.operation && (
            <p className="text-destructive text-sm">
              Unfinished {s.operation.name}
              {s.operation.failedStep && ` (stopped at ${s.operation.failedStep})`}
              {s.operation.error && `: ${s.operation.error}`}. Run Repair on the System page.
            </p>
          )}
          {problems.length > 0 ? (
            <ul className="grid gap-1.5">
              {problems.map((n) => (
                <li key={n.key} className="flex items-start gap-2 text-sm">
                  <CircleAlert className={`mt-0.5 size-4 shrink-0 ${n.level === 'error' ? 'text-destructive' : 'text-muted-foreground'}`} />
                  {n.message}
                </li>
              ))}
            </ul>
          ) : (
            !s.operation && (
              <p className="text-muted-foreground flex items-center gap-2 text-sm">
                {isUnhealthy(s, notices) ? (
                  <CircleAlert className="text-destructive size-4" />
                ) : (
                  <CircleCheck className="size-4 text-emerald-600 dark:text-emerald-500" />
                )}
                {s.stopped
                  ? 'Stopped on purpose.'
                  : s.locked
                    ? 'Waiting for the vault to be unlocked.'
                    : s.active === 'active' && s.healthy
                      ? 'Running and answering.'
                      : 'Not answering.'}
                {checkedAt && ` Last health check ${fmtDate(checkedAt)}.`}
              </p>
            )
          )}
        </div>

        <div className="grid gap-2">
          <h3 className="text-sm font-medium">Plugins</h3>
          <Problem error={pluginsError} />
          {plugins && plugins.length === 0 && <p className="text-muted-foreground text-sm">None installed. See the Shiver plugins page.</p>}
          {plugins && plugins.length > 0 && (
            <ul className="grid gap-1.5">
              {plugins.map((p) => {
                const next = latest(p.id);
                return (
                  <li key={p.id} className="flex flex-wrap items-center gap-x-2 text-sm">
                    <span>{p.name || p.id}</span>
                    <span className="text-muted-foreground">{p.version}</span>
                    {next && (
                      <a href={`#plugins/${s.id}`} className="text-primary inline-flex items-center gap-1 underline-offset-4 hover:underline">
                        <Info className="size-4" /> {next} available
                      </a>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </div>

        {changed.length > 0 && (
          <div className="grid gap-2">
            <h3 className="text-sm font-medium">Changed settings</h3>
            <dl className="grid gap-1 text-sm">
              {changed.map(([k, v]) => (
                <div key={k} className="flex flex-wrap gap-x-2">
                  <dt className="font-mono text-xs">{k}</dt>
                  <dd className="text-muted-foreground font-mono text-xs break-all">{v}</dd>
                </div>
              ))}
            </dl>
          </div>
        )}
      </div>
    </Section>
  );
}

// TokenDialog shows a secret token once: password again, vault passphrase.
function TokenDialog({ id, onClose }: { id: string; onClose: () => void }) {
  const [password, setPassword] = useState('');
  const [passphrase, setPassphrase] = useState('');
  const [token, setToken] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const show = async () => {
    setError(null);
    try {
      const r = await api.post<{ token: string }>('/api/token', { id, password, passphrase });
      setToken(r.token);
    } catch (e) {
      setError((e as ApiError).message);
    } finally {
      setPassword('');
      setPassphrase('');
    }
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{id}'s secret token</DialogTitle>
          <DialogDescription>Anyone with it can take over the server. It's hidden again when you close this.</DialogDescription>
        </DialogHeader>
        {token ? (
          <code className="bg-muted rounded-md p-3 font-mono text-sm break-all select-all">{token}</code>
        ) : (
          <form
            className="grid gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              show();
            }}
          >
            <div className="grid gap-1.5">
              <Label htmlFor="tp">Your password, again</Label>
              <Input id="tp" type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="tv">Vault passphrase</Label>
              <Input id="tv" type="password" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} />
            </div>
            {error && <p className="text-destructive text-sm">{error}</p>}
            <DialogFooter>
              <Button type="submit" disabled={!password || !passphrase}>
                Show
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
