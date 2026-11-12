import { CircleAlert, ExternalLink, Info } from 'lucide-react';
import type { PageProps } from '../App';
import { useActions } from '../actions';
import type { CatalogPlugin } from '../api';
import { Reload, Section, Table, Td } from '../common';
import { Badge } from '../components/badge';
import { Button } from '../components/button';
import { Card, CardContent } from '../components/card';
import { useData } from '../hooks';

// PluginsPage lists the Shiver plugins from plugins.json in the manager's
// repository, with each server's copy and an install or update button.
export function PluginsPage(_: PageProps) {
  const { run, jobsChanged } = useActions();
  const catalog = useData<CatalogPlugin[]>('catalog', jobsChanged);

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
  return (
    <>
      {catalog.loading && list.length === 0 && <p className="text-muted-foreground text-sm">Reading the plugin list…</p>}
      {list.map((p) => (
        <Section
          key={p.id}
          title={p.name}
          description={
            <>
              {p.description}{' '}
              <a
                className="inline-flex items-center gap-1 underline-offset-4 hover:underline"
                href={`https://github.com/${p.repo}`}
                target="_blank"
                rel="noreferrer"
              >
                {p.repo} <ExternalLink className="size-3" />
              </a>
            </>
          }
          actions={
            <>
              {p.latest ? <Badge variant="secondary">latest {p.latest}</Badge> : <Badge variant="destructive">latest unknown</Badge>}
              <Reload onClick={catalog.reload} loading={catalog.loading} />
            </>
          }
        >
          {p.error && <p className="text-destructive mb-3 text-sm">{p.error}</p>}
          <Table
            head={['Server', 'Installed', '']}
            empty={p.servers.length === 0 && <p className="text-muted-foreground py-6 text-center text-sm">No servers yet.</p>}
          >
            {p.servers.map((s) => {
              const verb = s.installed ? 'Update' : 'Install';
              return (
                <tr key={s.instance}>
                  <Td className="font-medium">{s.instance}</Td>
                  <Td className="text-muted-foreground">
                    {s.installed ? (
                      <span className="inline-flex items-center gap-1.5">
                        {s.installed}
                        {s.update && (
                          <span className="text-primary inline-flex items-center gap-1" title={`${p.latest} is out`}>
                            <Info className="size-4" /> {p.latest} available
                          </span>
                        )}
                      </span>
                    ) : (
                      'not installed'
                    )}
                  </Td>
                  <Td className="text-right">
                    {(!s.installed || s.update) && p.latest ? (
                      <Button
                        size="sm"
                        variant={s.update ? 'default' : 'outline'}
                        onClick={() =>
                          run({
                            action: 'plugin.add',
                            title: `${verb} ${p.name} on ${s.instance}`,
                            confirmLabel: verb,
                            description: `Backs up ${s.instance}, installs ${p.name} ${p.latest} from its latest release (checked against its checksum), and restarts the server. If Sharkord can’t load it, the previous copy comes back.`,
                            params: { id: s.instance, plugin: p.id }
                          })
                        }
                      >
                        {verb}
                      </Button>
                    ) : (
                      s.installed && <span className="text-muted-foreground text-xs">up to date</span>
                    )}
                  </Td>
                </tr>
              );
            })}
          </Table>
        </Section>
      ))}
      {list.length > 0 && (
        <p className="text-muted-foreground mb-6 text-sm">
          Remove a plugin in Sharkord’s own Settings &gt; Plugins. Another plugin can be installed from a file with a server’s menu (…) on the Servers page.
        </p>
      )}
    </>
  );
}
