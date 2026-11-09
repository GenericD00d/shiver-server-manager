import { useActions } from '../actions';
import { Problem, Reload, Section, Table, Td } from '../common';
import { Button } from '../components/button';
import { useData } from '../hooks';

type Rule = { port: number; proto: string; instance: string };

export function FirewallPage() {
  const { run, jobsChanged } = useActions();
  const fw = useData<{ firewall: string; rules: Rule[] }>('firewall', jobsChanged);
  return (
    <>
      <Section
        title="Firewall rules"
        description={`Firewall: ${fw.data?.firewall ?? '…'}. Caddy needs TCP 80 and 443 and UDP 443; each server its voice port over UDP and TCP. Servers' web ports stay closed.`}
        actions={
          <>
            <Reload onClick={fw.reload} loading={fw.loading} />
            <Button
              variant="outline"
              onClick={() => run({ action: 'firewall.repair', title: 'Repair firewall rules', description: 'Adds missing rules and removes leftovers that belong to no server.' })}
            >
              Repair
            </Button>
          </>
        }
      >
        <Problem error={fw.error} />
        <Table head={['Port', 'For']}>
          {(fw.data?.rules ?? []).map((r) => (
            <tr key={`${r.port}/${r.proto}`}>
              <Td className="font-mono">
                {r.port}/{r.proto}
              </Td>
              <Td>{r.instance}</Td>
            </tr>
          ))}
        </Table>
      </Section>
      <Section
        title="Router port mapping"
        description="For home servers: asks the router (UPnP) to forward the ports to this machine. Many routers ship with it off; the alternative is forwarding them by hand."
      >
        <div className="flex flex-wrap gap-2">
          {(['status', 'on', 'off'] as const).map((mode) => (
            <Button key={mode} variant="outline" onClick={() => run({ action: 'firewall.upnp', title: `Router port mapping: ${mode}`, params: { mode } })}>
              {mode === 'status' ? 'Check the router' : `Turn ${mode}`}
            </Button>
          ))}
        </div>
      </Section>
      <Section title="Cloud firewalls" description="A provider's firewall or security group needs the same ports opened in its control panel.">
        <p className="text-sm">Allow incoming TCP 80 and 443, UDP 443, and TCP and UDP on the voice ports (40000-40999 by default).</p>
      </Section>
    </>
  );
}
