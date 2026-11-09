import { HardDrive, Plus } from 'lucide-react';
import { useActions } from '../actions';
import { fmtBytes, Problem, Reload, Section, Table, Td } from '../common';
import { Badge } from '../components/badge';
import { Button } from '../components/button';
import { useData } from '../hooks';

type Location = {
  path: string;
  default?: boolean;
  fsUUID?: string;
  mount: { target: string; source: string; fstype: string };
  freeBytes: number;
  servers: string[] | null;
  problem?: string;
};
type Drive = { path: string; size: string; fstype: string; label: string; mountPoint: string };

export function StoragePage() {
  const { run, jobsChanged } = useActions();
  const storage = useData<{ locations: Location[]; backupLocation: string }>('storage', jobsChanged);
  const drives = useData<Drive[]>('drives', jobsChanged);
  const locs = storage.data?.locations ?? [];

  return (
    <>
      <Section
        title="Storage locations"
        description="Folders on drives that come back after a reboot (listed in /etc/fstab), formatted ext4, XFS or Btrfs, never noexec."
        actions={
          <>
            <Reload onClick={storage.reload} loading={storage.loading} />
            <Button
              variant="outline"
              onClick={() =>
                run({
                  action: 'storage.add',
                  title: 'Add a storage location',
                  fields: [{ name: 'path', label: 'Folder', required: true, placeholder: '/srv/disk2/sharkord' }]
                })
              }
            >
              <Plus /> Add a folder
            </Button>
          </>
        }
      >
        <Problem error={storage.error} />
        <Table head={['Location', 'Drive', 'File system', 'Free', 'Servers', '']}>
          {locs.map((l) => (
            <tr key={l.path}>
              <Td className="font-medium">
                {l.path} {l.default && <Badge variant="secondary">default</Badge>}
                {l.problem && <p className="text-destructive text-xs">{l.problem}</p>}
              </Td>
              <Td className="text-muted-foreground">{l.mount.source}</Td>
              <Td className="text-muted-foreground">{l.mount.fstype}</Td>
              <Td>{fmtBytes(l.freeBytes)}</Td>
              <Td className="text-muted-foreground">{(l.servers ?? []).join(', ') || '–'}</Td>
              <Td className="text-right whitespace-nowrap">
                {!l.default && (
                  <>
                    <Button variant="ghost" size="sm" onClick={() => run({ action: 'storage.default', title: `Make ${l.path} the default`, params: { path: l.path } })}>
                      Make default
                    </Button>
                    {(l.servers ?? []).length === 0 && (
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() =>
                          run({ action: 'storage.remove', title: `Remove ${l.path}`, description: 'Only the location goes; the folder stays.', params: { path: l.path } })
                        }
                      >
                        Remove
                      </Button>
                    )}
                  </>
                )}
              </Td>
            </tr>
          ))}
        </Table>
      </Section>

      <Section
        title="New drives"
        description="Formatted drives that aren't mounted yet. Formatting is a step outside the manager (see System → Help)."
        actions={<Reload onClick={drives.reload} loading={drives.loading} />}
      >
        <Problem error={drives.error} />
        {(drives.data ?? []).length === 0 ? (
          <p className="text-muted-foreground text-sm">None found.</p>
        ) : (
          <Table head={['Drive', 'Size', 'File system', 'Label', '']}>
            {drives.data!.map((d) => (
              <tr key={d.path}>
                <Td className="font-medium">{d.path}</Td>
                <Td>{d.size}</Td>
                <Td>{d.fstype}</Td>
                <Td className="text-muted-foreground">{d.label}</Td>
                <Td className="text-right">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() =>
                      run({
                        action: 'storage.mount',
                        title: `Use ${d.path}`,
                        description: `Adds it to /etc/fstab (with nofail, so a missing drive can't stop the server booting), mounts it at ${d.mountPoint}, and adds ${d.mountPoint}/sharkord as a location.`,
                        params: { device: d.path },
                        confirmLabel: 'Mount and add'
                      })
                    }
                  >
                    <HardDrive /> Mount and add
                  </Button>
                </Td>
              </tr>
            ))}
          </Table>
        )}
      </Section>

      <Section
        title="Backup location"
        description={`Backups and exports go to ${storage.data?.backupLocation ?? '…'}. Any local drive will do, ideally a different one from the data.`}
        actions={
          <Button
            variant="outline"
            onClick={() => run({ action: 'storage.backups', title: 'Backup location', fields: [{ name: 'path', label: 'Folder', required: true }] })}
          >
            Change
          </Button>
        }
      >
        <p className="text-muted-foreground text-sm">Move a server between locations from its menu on the Servers page.</p>
      </Section>
    </>
  );
}
