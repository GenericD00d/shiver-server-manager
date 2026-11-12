import { Archive, BrickWall, HardDrive, LoaderCircle, LogOut, Puzzle, Server, ShieldCheck, ShieldEllipsis, Wrench } from 'lucide-react';
import { useEffect, useState, type ComponentType } from 'react';
import { ActionProvider } from './actions';
import { api, setOnLoggedOut, type Me, type Meta } from './api';
import { Button } from './components/button';
import { Login } from './Login';
import { BackupsPage } from './pages/Backups';
import { CertificatesPage } from './pages/Certificates';
import { FirewallPage } from './pages/Firewall';
import { PluginsPage } from './pages/Plugins';
import { SecurityPage } from './pages/Security';
import { ServersPage } from './pages/Servers';
import { StoragePage } from './pages/Storage';
import { SystemPage } from './pages/System';

export type PageProps = { meta: Meta };

const pages: { id: string; title: string; icon: ComponentType<{ className?: string }>; Page: ComponentType<PageProps> }[] = [
  { id: 'servers', title: 'Servers', icon: Server, Page: ServersPage },
  { id: 'plugins', title: 'Shiver plugins', icon: Puzzle, Page: PluginsPage },
  { id: 'certificates', title: 'Certificates', icon: ShieldCheck, Page: CertificatesPage },
  { id: 'backups', title: 'Backups', icon: Archive, Page: BackupsPage },
  { id: 'storage', title: 'Storage', icon: HardDrive, Page: StoragePage },
  { id: 'firewall', title: 'Firewall', icon: BrickWall, Page: FirewallPage },
  { id: 'security', title: 'Security', icon: ShieldEllipsis, Page: SecurityPage },
  { id: 'system', title: 'System', icon: Wrench, Page: SystemPage }
];

const currentPage = () => {
  const id = window.location.hash.replace(/^#\/?/, '');
  return pages.some((p) => p.id === id) ? id : 'servers';
};

export function App() {
  const [me, setMe] = useState<Me | null | undefined>(undefined);
  const [meta, setMeta] = useState<Meta | null>(null);
  const [page, setPage] = useState(currentPage);

  useEffect(() => {
    setOnLoggedOut(() => setMe(null));
    api.get<Me>('/api/me').then(setMe, () => setMe(null));
    const onHash = () => setPage(currentPage());
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);

  useEffect(() => {
    if (me) api.get<Meta>('/api/meta').then(setMeta, () => {});
  }, [me]);

  if (me === undefined) {
    return (
      <div className="flex min-h-svh items-center justify-center">
        <LoaderCircle className="text-muted-foreground animate-spin" />
      </div>
    );
  }
  if (me === null) return <Login onLoggedIn={setMe} />;

  const current = pages.find((p) => p.id === page)!;
  const reauth = Object.fromEntries(Object.entries(meta?.actions ?? {}).map(([k, v]) => [k, v.reauth]));

  return (
    <ActionProvider reauth={reauth}>
      <div className="flex min-h-svh flex-col md:flex-row">
        <aside className="bg-sidebar text-sidebar-foreground border-sidebar-border flex shrink-0 flex-col border-b md:w-56 md:border-r md:border-b-0">
          <div className="flex items-center gap-2 px-4 py-4">
            <img src="/logo.png" alt="" className="size-8 rounded-full" />
            <div className="leading-tight">
              <div className="text-sm font-semibold">Shiver Server Manager</div>
              <div className="text-muted-foreground text-xs">{me.version}</div>
            </div>
            <Button
              className="ml-auto md:hidden"
              variant="ghost"
              size="icon"
              title={`Log out ${me.user}`}
              onClick={() => api.post('/api/logout').finally(() => setMe(null))}
            >
              <LogOut />
            </Button>
          </div>
          <nav className="flex gap-1 overflow-x-auto px-2 pb-2 md:flex-col md:overflow-visible">
            {pages.map((p) => (
              <a
                key={p.id}
                href={`#/${p.id}`}
                className={`flex shrink-0 items-center gap-2 rounded-md px-3 py-2 text-sm ${
                  p.id === page ? 'bg-sidebar-accent text-sidebar-accent-foreground font-medium' : 'text-muted-foreground hover:bg-sidebar-accent'
                }`}
              >
                <p.icon className="size-4" />
                {p.title}
              </a>
            ))}
          </nav>
          <div className="mt-auto hidden items-center justify-between gap-2 border-t px-4 py-3 md:flex">
            <span className="text-muted-foreground truncate text-sm">{me.user}</span>
            <Button variant="ghost" size="icon" title="Log out" onClick={() => api.post('/api/logout').finally(() => setMe(null))}>
              <LogOut />
            </Button>
          </div>
        </aside>
        <main className="min-w-0 flex-1 p-4 md:p-8">
          <div className="mx-auto max-w-6xl">
            <h1 className="mb-6 text-2xl font-semibold">{current.title}</h1>
            {meta ? <current.Page meta={meta} /> : <LoaderCircle className="text-muted-foreground animate-spin" />}
          </div>
        </main>
      </div>
    </ActionProvider>
  );
}
