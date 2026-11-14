import { CircleAlert, ExternalLink, Info } from 'lucide-react';
import { useEffect, useState } from 'react';
import type { PageProps } from '../App';
import { useActions } from '../actions';
import type { CatalogPlugin } from '../api';
import { Reload, Section, Table, Td } from '../common';
import { Button } from '../components/button';
import { Card, CardContent } from '../components/card';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../components/select';
import { useData } from '../hooks';

// The server named in the address (#plugins/chat-a), if any.
const serverInHash = () => decodeURIComponent(window.location.hash.replace(/^#\/?/, '').split('/')[1] ?? '');

// PluginsPage shows one server's Shiver plugins (from plugins.json in the
// manager's repository), picked from a list, with install and update
// buttons.
export function PluginsPage(_: PageProps) {
  const { run, jobsChanged } = useActions();
  const catalog = useData<CatalogPlugin[]>('catalog', jobsChanged);
  const [picked, setPicked] = useState(serverInHash);
  // The plugins ticked for "Install selected", on the server shown.
  const [ticked, setTicked] = useState<string[]>([]);

  useEffect(() => {
    const onHash = () => setPicked(serverInHash());
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);

  if (catalog.error) {
    return (
      <Card className="mb-6">
        <CardContent className="flex items-start gap-3 py-6">
          <CircleAlert className="text-destructive mt-0.5 size-5 shrink-0" />
          <div className="grid gap-1">
            <p className="font-medium">Shiver plugins are unavailable</p>
            <p className="text-muted-foreground text-sm">
              The plugin list couldn’t be read, so nothing can be installed from here right now.{' '}
              {catalog.error.replace(/^Shiver plugins are unavailable: /, '')}
            </p>
            <div>
              <Button variant="outline" size="sm" className="mt-2" onClick={catalog.reload}>
                Try again
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>
    );
  }

  const list = catalog.data ?? [];
  // Every plugin lists every server, in the same order.
  const servers = list[0]?.servers.map((s) => s.instance) ?? [];
  const server = servers.includes(picked) ? picked : servers[0];

  // What can be installed or updated on the server shown.
  const offered = list.filter((p) => {
    const s = p.servers.find((x) => x.instance === server);
    return s && (!s.installed || s.update) && p.latest;
  });
  const selected = offered.filter((p) => ticked.includes(p.id));
  const tick = (id: string, on: boolean) => setTicked((t) => (on ? [...t, id] : t.filter((x) => x !== id)));

  const pick = (id: string) => {
    setPicked(id);
    setTicked([]);
    window.history.replaceState(null, '', `#plugins/${encodeURIComponent(id)}`);
  };

  return (
    <>
      <Section
        title={server ? `Plugins on ${server}` : 'Shiver plugins'}
        description="Pick a server to see which Shiver plugins it has, and install or update them: one at a time, or tick several and install them together with one restart."
        actions={
          <>
            {servers.length > 0 && (
              <Select value={server} onValueChange={pick}>
                <SelectTrigger className="w-48" aria-label="Server">
                  <SelectValue placeholder="Choose a server" />
                </SelectTrigger>
                <SelectContent>
                  {servers.map((id) => {
                    const updates = list.some((p) => p.servers.some((s) => s.instance === id && s.update));
                    return (
                      <SelectItem key={id} value={id}>
                        <span className="inline-flex items-center gap-1.5">
                          {id}
                          {updates && <Info className="text-primary size-4" aria-label="updates available" />}
                        </span>
                      </SelectItem>
                    );
                  })}
                </SelectContent>
              </Select>
            )}
            {selected.length > 0 && (
              <Button
                size="sm"
                onClick={() =>
                  run({
                    action: 'plugin.add',
                    title: `Install ${selected.length === 1 ? selected[0].name : `${selected.length} plugins`} on ${server}`,
                    confirmLabel: 'Install',
                    description: `Backs up ${server} once, installs or updates ${selected.map((p) => `${p.name} ${p.latest}`).join(', ')} from their latest releases (each checked against its checksum), and restarts the server once. If Sharkord can’t load one, the previous copies all come back.`,
                    params: { id: server, plugin: selected.map((p) => p.id).join(',') }
                  })
                }
              >
                Install selected ({selected.length})
              </Button>
            )}
            <Reload onClick={catalog.reload} loading={catalog.loading} />
          </>
        }
      >
        {catalog.loading && list.length === 0 ? (
          <p className="text-muted-foreground text-sm">Reading the plugin list…</p>
        ) : !server ? (
          <p className="text-muted-foreground py-6 text-center text-sm">No servers yet.</p>
        ) : (
          <Table head={['Plugin', 'Installed', 'Latest', '']}>
            {list.map((p) => {
              const s = p.servers.find((x) => x.instance === server);
              const verb = s?.installed ? 'Update' : 'Install';
              return (
                <tr key={p.id}>
                  <Td>
                    <div className="grid grid-cols-[auto_1fr] items-start gap-x-2 gap-y-0.5">
                      {offered.includes(p) ? (
                        <input
                          type="checkbox"
                          className="accent-primary mt-0.5 size-4"
                          aria-label={`Select ${p.name}`}
                          checked={ticked.includes(p.id)}
                          onChange={(e) => tick(p.id, e.target.checked)}
                        />
                      ) : (
                        <span className="size-4" />
                      )}
                      <span className="font-medium">{p.name}</span>
                      <span className="text-muted-foreground col-start-2 text-xs">
                        {p.description}{' '}
                        <a
                          className="inline-flex items-center gap-1 underline-offset-4 hover:underline"
                          href={`https://github.com/${p.repo}`}
                          target="_blank"
                          rel="noreferrer"
                        >
                          {p.repo} <ExternalLink className="size-3" />
                        </a>
                      </span>
                      {p.error && <span className="text-destructive col-start-2 text-xs">{p.error}</span>}
                    </div>
                  </Td>
                  <Td className="text-muted-foreground whitespace-nowrap">
                    {s?.installed ? (
                      <span className="inline-flex items-center gap-1.5">
                        {s.installed}
                        {s.update && <Info className="text-primary size-4" aria-label={`${p.latest} is out`} />}
                      </span>
                    ) : (
                      'not installed'
                    )}
                  </Td>
                  <Td className="text-muted-foreground whitespace-nowrap">{p.latest ?? 'unknown'}</Td>
                  <Td className="text-right">
                    {s && (!s.installed || s.update) && p.latest ? (
                      <Button
                        size="sm"
                        variant={s.update ? 'default' : 'outline'}
                        onClick={() =>
                          run({
                            action: 'plugin.add',
                            title: `${verb} ${p.name} on ${server}`,
                            confirmLabel: verb,
                            description: `Backs up ${server}, installs ${p.name} ${p.latest} from its latest release (checked against its checksum), and restarts the server. If Sharkord can’t load it, the previous copy comes back.`,
                            params: { id: server, plugin: p.id }
                          })
                        }
                      >
                        {verb}
                      </Button>
                    ) : (
                      s?.installed && <span className="text-muted-foreground text-xs">up to date</span>
                    )}
                  </Td>
                </tr>
              );
            })}
          </Table>
        )}
      </Section>
      {list.length > 0 && (
        <p className="text-muted-foreground mb-6 text-sm">
          Remove a plugin in Sharkord’s own Settings &gt; Plugins. Another plugin can be installed from a file with a server’s menu (…) on the Servers page.
        </p>
      )}
    </>
  );
}
