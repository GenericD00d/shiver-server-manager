import { Activity, Download, KeyRound, Wrench } from "lucide-react";
import { useEffect, useState } from "react";
import type { PageProps } from "../App";
import { JobOutput, useActions } from "../actions";
import { api, type Health, type Job } from "../api";
import {
  fmtDate,
  Notices,
  Problem,
  Reload,
  Section,
  Table,
  Td,
} from "../common";
import { Button } from "../components/button";
import { useData } from "../hooks";
import { Markdown } from "../markdown";

type UpdateCheck = {
  current: string;
  latest?: string;
  available: boolean;
  error?: string;
  hasToken: boolean;
};

type Finding = {
  kind: string;
  instance?: string;
  what: string;
  ask: boolean;
  data: boolean;
};

export function SystemPage({ meta }: PageProps) {
  const { run, jobsChanged } = useActions();
  const health = useData<Health>("health", jobsChanged);
  const plan = useData<Finding[] | null>("repair", jobsChanged);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [shown, setShown] = useState<Job | null>(null);

  useEffect(() => {
    api.get<Job[]>("/api/jobs").then(setJobs, () => {});
  }, [jobsChanged]);

  return (
    <>
      <Section
        title="Health"
        description={
          health.data?.checkedAt
            ? `Last checked ${fmtDate(health.data.checkedAt)} (every 5 minutes).`
            : "Not checked yet."
        }
        actions={
          <>
            <Reload onClick={health.reload} loading={health.loading} />
            <Button
              onClick={() =>
                run({ action: "system.health", title: "Health check" })
              }
            >
              <Activity /> Check now
            </Button>
          </>
        }
      >
        {(health.data?.notices ?? []).length === 0 ? (
          <p className="text-muted-foreground text-sm">
            Everything is healthy.
          </p>
        ) : (
          <Notices notices={health.data!.notices!} />
        )}
      </Section>

      <Updates />

      <Section
        title="Repair"
        description="Makes the system match the records: finishes interrupted operations, redoes anything missing, removes leftovers. Data folders are only ever removed from the terminal, with an explicit yes."
        actions={
          <>
            <Reload onClick={plan.reload} loading={plan.loading} />
            <Button
              disabled={(plan.data ?? []).length === 0}
              onClick={() => run({ action: "system.repair", title: "Repair" })}
            >
              <Wrench /> Repair
            </Button>
          </>
        }
      >
        <Problem error={plan.error} />
        {(plan.data ?? []).length === 0 ? (
          <p className="text-muted-foreground text-sm">
            Everything matches the records.
          </p>
        ) : (
          <ul className="grid gap-1 text-sm">
            {plan.data!.map((f, i) => (
              <li key={i}>
                <span className="text-muted-foreground">{f.kind}:</span>{" "}
                {f.instance && <b>{f.instance}: </b>}
                {f.what}
              </li>
            ))}
          </ul>
        )}
      </Section>

      <Section
        title="Recent actions"
        description="Everything run from this page since the web service started. All actions are also logged in /var/log/shiver-server-manager.log."
      >
        <Table
          head={["Action", "By", "Started", "Result", ""]}
          empty={
            jobs.length === 0 && (
              <p className="text-muted-foreground py-6 text-center text-sm">
                Nothing run from here yet.
              </p>
            )
          }
        >
          {jobs.map((j) => (
            <tr key={j.id}>
              <Td className="font-medium">{j.title}</Td>
              <Td className="text-muted-foreground">{j.user}</Td>
              <Td className="text-muted-foreground">{fmtDate(j.started)}</Td>
              <Td>
                {!j.done
                  ? "running"
                  : j.exitCode === 0
                    ? "done"
                    : `failed (${j.exitCode})`}
              </Td>
              <Td className="text-right">
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => setShown(shown?.id === j.id ? null : j)}
                >
                  {shown?.id === j.id ? "Hide" : "Output"}
                </Button>
              </Td>
            </tr>
          ))}
        </Table>
        {shown && (
          <div className="mt-4">
            <JobOutput key={shown.id} job={shown} />
          </div>
        )}
      </Section>

      <Section title="Maintenance">
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            onClick={() =>
              run({
                action: "system.setup",
                title: "First-time setup",
                description: "Checks the system again; safe to run any time.",
              })
            }
          >
            Run setup again
          </Button>
          <Button
            variant="destructive"
            onClick={() =>
              run({
                action: "system.removeAll",
                title: "Remove everything",
                destructive: true,
                confirmLabel: "Delete every server",
                description:
                  "Deletes every server and all their data, for good, before uninstalling. The vault, records and backups stay.",
                fields: [
                  {
                    name: "confirm",
                    label: 'Type "remove everything" to confirm',
                    required: true,
                  },
                ],
              })
            }
          >
            Remove everything
          </Button>
        </div>
      </Section>

      <Section
        title="Help"
        description={`Steps outside the manager. Shiver Server Manager ${meta.version}.`}
      >
        <Markdown text={meta.help} />
      </Section>
    </>
  );
}

// Updates offers the manager's latest release. After starting the install,
// it waits for the web service to come back on the new version (or to have
// forgotten the session, as a restart does) and reloads the page.
function Updates() {
  const { run, jobsChanged } = useActions();
  const check = useData<UpdateCheck>("update", jobsChanged);
  const [waiting, setWaiting] = useState(false);
  const c = check.data;

  useEffect(() => {
    if (!waiting || !c) return;
    const started = Date.now();
    const timer = setInterval(async () => {
      if (Date.now() - started > 5 * 60_000) {
        clearInterval(timer);
        setWaiting(false);
        return;
      }
      try {
        const res = await fetch("/api/me", { credentials: "same-origin" });
        if (res.status === 401) window.location.reload();
        else if (
          res.ok &&
          ((await res.json()) as { version: string }).version !== c.current
        )
          window.location.reload();
      } catch {
        // restarting
      }
    }, 3000);
    return () => clearInterval(timer);
  }, [waiting, c]);

  const tokenAction = () =>
    run({
      action: "manager.token",
      title: "GitHub token for updates",
      description:
        'Needed while the repository is private. Make a fine-grained token at github.com/settings/personal-access-tokens with read-only "Contents" access to the shiver-server-manager repository only. It is kept readable by root only.',
      fields: [
        { name: "token", label: "Token", type: "password" },
        {
          name: "clear",
          label: "Remove the saved token instead",
          type: "switch",
        },
      ],
    });

  return (
    <Section
      title="Updates"
      description={c ? `This is version ${c.current}.` : undefined}
      actions={
        <>
          <Reload onClick={check.reload} loading={check.loading} />
          <Button variant="outline" onClick={tokenAction}>
            <KeyRound /> GitHub token
          </Button>
          <Button
            disabled={!c?.available}
            onClick={() => {
              run({
                action: "manager.update",
                title: `Update to ${c?.latest}`,
                description:
                  "Downloads the package from GitHub, checks it, and installs it. Servers keep running. The web page restarts, so you log in again afterwards.",
                confirmLabel: "Update",
              });
              setWaiting(true);
            }}
          >
            <Download /> {c?.available ? `Update to ${c.latest}` : "Update"}
          </Button>
        </>
      }
    >
      <Problem error={check.error} />
      {c?.error ? (
        <p className="text-destructive text-sm">{c.error}</p>
      ) : c?.available ? (
        <p className="text-sm">
          Version {c.latest} is out.
          {waiting && " Waiting for the web page to restart on it..."}
        </p>
      ) : (
        c && (
          <p className="text-muted-foreground text-sm">
            {c.current} is the latest version.
          </p>
        )
      )}
    </Section>
  );
}
