import { UserPlus } from 'lucide-react';
import { useActions } from '../actions';
import { Problem, Reload, Section, Table, Td } from '../common';
import { Button } from '../components/button';
import { useData } from '../hooks';

type Alerts = { configured: boolean; webhook: string; emailTo: string; smtp: string; smtpUser: string; emailFrom: string };

export function SecurityPage() {
  const { run, jobsChanged } = useActions();
  const admins = useData<{ admins: string[] }>('admins', jobsChanged);
  const alerts = useData<Alerts>('alerts', jobsChanged);
  const pass = (name: string, label: string) => ({ name, label, type: 'password' as const, required: true });

  return (
    <>
      <Section
        title="Admins"
        description="OS accounts in the shiver-admins group. Create the account on the server first (sudo adduser <name>)."
        actions={
          <>
            <Reload onClick={admins.reload} loading={admins.loading} />
            <Button onClick={() => run({ action: 'admins.add', title: 'Add an admin', fields: [{ name: 'user', label: 'OS username', required: true }] })}>
              <UserPlus /> Add
            </Button>
          </>
        }
      >
        <Problem error={admins.error} />
        <Table head={['Account', '']}>
          {(admins.data?.admins ?? []).map((u) => (
            <tr key={u}>
              <Td className="font-medium">{u}</Td>
              <Td className="text-right">
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => run({ action: 'admins.remove', title: `Remove ${u}`, description: 'The last admin can’t be removed.', params: { user: u } })}
                >
                  Remove
                </Button>
              </Td>
            </tr>
          ))}
        </Table>
      </Section>

      <Section title="Vault" description="Keeps secret tokens and data-folder keys. Backups can be restored with its passphrase or the recovery key.">
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            onClick={() =>
              run({ action: 'vault.unlock', title: 'Unlock the vault', description: 'Opens and starts encrypted servers waiting since a reboot.', fields: [pass('passphrase', 'Vault passphrase')] })
            }
          >
            Unlock
          </Button>
          <Button
            variant="outline"
            onClick={() =>
              run({
                action: 'vault.change',
                title: 'Change the vault passphrase',
                description: 'The recovery key stays the same.',
                fields: [pass('passphrase', 'Current passphrase'), pass('newPassphrase', 'New passphrase (12+ characters)')]
              })
            }
          >
            Change passphrase
          </Button>
          <Button
            variant="outline"
            onClick={() =>
              run({
                action: 'vault.recover',
                title: 'Recover the vault',
                description: 'With the recovery key you saved at setup (AGE-SECRET-KEY-1…), and a new passphrase.',
                fields: [pass('recoveryKey', 'Recovery key'), pass('newPassphrase', 'New passphrase (12+ characters)')]
              })
            }
          >
            Recover with the recovery key
          </Button>
        </div>
      </Section>

      <Section
        title="Health alerts"
        description="The health check runs every 5 minutes and sends a message when a problem appears or clears."
        actions={<Reload onClick={alerts.reload} loading={alerts.loading} />}
      >
        <Problem error={alerts.error} />
        {alerts.data && (
          <div className="grid gap-4">
            <p className="text-sm">
              {!alerts.data.configured
                ? 'Alerts are off.'
                : [alerts.data.webhook && `Webhook: ${alerts.data.webhook}`, alerts.data.emailTo && `Email to ${alerts.data.emailTo} via ${alerts.data.smtp}`]
                    .filter(Boolean)
                    .join(' · ')}
            </p>
            <div className="flex flex-wrap gap-2">
              <Button
                variant="outline"
                onClick={() =>
                  run({
                    action: 'alerts.set',
                    title: 'Health alerts',
                    description: 'A webhook gets a JSON POST with "text" and "content" (Slack, Mattermost, Discord). Fill in either or both.',
                    fields: [
                      { name: 'webhook', label: 'Webhook URL', initial: alerts.data!.webhook },
                      { name: 'emailTo', label: 'Email to', initial: alerts.data!.emailTo },
                      { name: 'smtp', label: 'Mail server (host:port)', initial: alerts.data!.smtp },
                      { name: 'smtpUser', label: 'Mail server login', initial: alerts.data!.smtpUser },
                      { name: 'smtpPassword', label: 'Mail server password', type: 'password' },
                      { name: 'emailFrom', label: 'Sender (default: the login)', initial: alerts.data!.emailFrom }
                    ]
                  })
                }
              >
                Set up
              </Button>
              {alerts.data.configured && (
                <>
                  <Button variant="outline" onClick={() => run({ action: 'alerts.test', title: 'Send a test alert' })}>
                    Send a test
                  </Button>
                  <Button variant="ghost" onClick={() => run({ action: 'alerts.set', title: 'Turn alerts off', params: { off: 'true' } })}>
                    Turn off
                  </Button>
                </>
              )}
            </div>
          </div>
        )}
      </Section>
    </>
  );
}
