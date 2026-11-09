import { Ellipsis, Eye, Import, Plus } from 'lucide-react';
import { useState } from 'react';
import type { PageProps } from '../App';
import { useActions, type ActionSpec } from '../actions';
import { api, ApiError, type Health, type Server } from '../api';
import { Problem, Reload, Section, StateBadge, Table, Td, Notices } from '../common';
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
  const [tokenFor, setTokenFor] = useState<string | null>(null);

  const createSpec: ActionSpec = {
    action: 'server.create',
    title: 'Create a server',
    description: "Point the domain's A record at this server first. Everything else has a default.",
    fields: [
      { name: 'id', label: 'Server ID', required: true, help: 'Lowercase letters, digits and hyphens, starting with a letter; up to 20.' },
      { name: 'domain', label: 'Domain', required: true, placeholder: 'chat.example.com' },
      { name: 'tls', label: 'Certificate', type: 'select', options: tlsOptions, initial: 'letsencrypt', required: true },
      { name: 'encrypt', label: 'Encrypted data folder', type: 'switch', initial: 'true', help: 'Protects a stolen disk; after a reboot, the server waits for the vault to be unlocked.' }
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
      { name: 'passphrase', label: "Export's passphrase (or the vault's, for a backup)", type: 'password' },
      { name: 'token', label: "The old server's secret token (optional)", type: 'password', help: 'An export with the token inside doesn’t need it.' },
      { name: 'encrypt', label: 'Encrypted data folder', type: 'switch', initial: 'true' }
    ]
  };

  const list = servers.data ?? [];
  return (
    <>
      <Notices notices={(health.data?.notices ?? []).filter((n) => n.level !== 'info' || n.key.startsWith('locked'))} />
      <Section
        title="Your servers"
        description="Each is a Sharkord community on its own domain."
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
          {list.map((s) => (
            <tr key={s.id}>
              <Td className="font-medium">{s.id}</Td>
              <Td>
                <StateBadge s={s} />
              </Td>
              <Td>
                <a className="underline-offset-4 hover:underline" href={`https://${s.domain}`} target="_blank" rel="noreferrer">
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
                <ServerMenu s={s} meta={meta} onShowToken={() => setTokenFor(s.id)} />
              </Td>
            </tr>
          ))}
        </Table>
      </Section>
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
          ? item('Unlock (vault)', { action: 'vault.unlock', title: 'Unlock the vault', description: 'Opens every encrypted server waiting since a reboot, and starts it.', fields: [passphrase] })
          : s.active === 'active'
            ? item('Stop', { action: 'server.stop', title: `Stop ${s.id}`, description: 'It stays stopped until you start it; repair and the health check leave it alone.' })
            : item('Start', { action: 'server.start', title: `Start ${s.id}` })}
        {item('Restart', { action: 'server.restart', title: `Restart ${s.id}` })}
        {item('Change a setting', {
          action: 'server.set',
          title: `Change a setting on ${s.id}`,
          description: 'Sharkord’s settings; the server restarts. Leave the value empty to go back to the default.',
          fields: [
            { name: 'key', label: 'Setting', type: 'select', required: true, options: meta.settings.map((x) => ({ value: x.Name, label: `${x.Name} — ${x.Help}` })) },
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
          fields: [
            { name: 'to', label: 'Storage location', required: true, placeholder: '/srv/sharkord-disk2/sharkord' },
            ...(s.encrypted ? [passphrase] : [])
          ]
        })}
        {s.encrypted &&
          item('Grow storage', {
            action: 'server.grow',
            title: `Grow ${s.id}'s container`,
            fields: [{ name: 'size', label: 'New size (GiB)', type: 'number', required: true }, passphrase]
          })}
        <DropdownMenuSeparator />
        {item('Install or update Shiver', { action: 'shiver.install', title: `Shiver on ${s.id}`, description: 'Backs up, installs the latest Shiver plugin, and restarts; the previous copy comes back if Sharkord can’t load it.' })}
        <DropdownMenuItem onSelect={onShowToken}>
          <Eye /> Show secret token
        </DropdownMenuItem>
        {item('Save secret token', {
          action: 'token.save',
          title: `Save ${s.id}'s secret token`,
          description: 'Checked against the server’s database first.',
          fields: [{ name: 'token', label: 'Secret token', type: 'password', required: true }]
        })}
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
