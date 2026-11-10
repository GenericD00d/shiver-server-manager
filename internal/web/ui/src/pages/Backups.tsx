import { Archive, CalendarClock, Trash2 } from "lucide-react";
import { useActions } from "../actions";
import type { Server } from "../api";
import {
  fmtBytes,
  fmtDate,
  Problem,
  Reload,
  Section,
  serverOptions,
  Table,
  Td,
} from "../common";
import { Button } from "../components/button";
import { useData } from "../hooks";

type Entry = { file: string; createdAt: string; size: number; sha256: string };
type Policy = {
  instance: string;
  off: boolean;
  times: string[];
  days: string[];
  keepCount: number;
  keepDays: number;
  default: boolean;
  next?: string;
  last?: string;
  backups: number;
};

const describeSchedule = (p: Policy) =>
  p.off
    ? "off"
    : `${p.times.join(", ")}, ${p.days.length ? p.days.join(", ") : "every day"}`;
const describeKeep = (p: Policy) =>
  [
    p.keepCount > 0 && `the last ${p.keepCount}`,
    p.keepDays > 0 && `${p.keepDays} days`,
  ]
    .filter(Boolean)
    .join(", at most ") || "all";

export function BackupsPage() {
  const { run, jobsChanged } = useActions();
  const backups = useData<Record<string, Entry[] | null>>(
    "backups",
    jobsChanged,
  );
  const policies = useData<Policy[] | null>("backupSchedules", jobsChanged);
  const servers = useData<Server[]>("servers", jobsChanged);
  const storage = useData<{ backupLocation: string }>("storage", jobsChanged);
  const location = storage.data?.backupLocation ?? "/var/backups/sharkord";

  const rows = Object.entries(backups.data ?? {})
    .flatMap(([id, list]) => (list ?? []).map((e) => ({ id, ...e })))
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt));

  return (
    <>
      <Section
        title="Backups"
        description={`Encrypted to the vault's key; restoring needs the vault passphrase. They're in ${location}.`}
        actions={
          <>
            <Reload onClick={backups.reload} loading={backups.loading} />
            <Button
              onClick={() =>
                run({
                  action: "server.backup",
                  title: "Back up now",
                  fields: [
                    {
                      name: "id",
                      label: "Server",
                      type: "select",
                      required: true,
                      options: serverOptions(servers.data, true),
                    },
                  ],
                })
              }
            >
              <Archive /> Back up now
            </Button>
          </>
        }
      >
        <Problem error={backups.error} />
        <Table
          head={["Server", "Taken", "Size", ""]}
          empty={
            rows.length === 0 &&
            !backups.loading && (
              <p className="text-muted-foreground py-6 text-center text-sm">
                No backups yet.
              </p>
            )
          }
        >
          {rows.map((r) => (
            <tr key={r.id + r.file}>
              <Td className="font-medium">{r.id}</Td>
              <Td>{fmtDate(r.createdAt)}</Td>
              <Td className="text-muted-foreground">{fmtBytes(r.size)}</Td>
              <Td className="text-right">
                <div className="flex justify-end gap-2">
                  <Button
                    variant="ghost"
                    size="sm"
                    title={`Delete this backup of ${r.id}`}
                    onClick={() =>
                      run({
                        action: "backup.delete",
                        title: `Delete a backup of ${r.id}`,
                        destructive: true,
                        confirmLabel: "Delete",
                        description: `Deletes the backup from ${fmtDate(r.createdAt)} for good. It can't be restored afterwards.`,
                        params: { id: r.id, file: r.file },
                      })
                    }
                  >
                    <Trash2 />
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() =>
                      run({
                        action: "server.restore",
                        title: `Restore ${r.id}`,
                        destructive: true,
                        confirmLabel: "Restore",
                        description: `Replaces ${r.id}'s data with this backup from ${fmtDate(r.createdAt)}. Everything since is lost; if the server doesn't start, the current data comes back.`,
                        params: {
                          id: r.id,
                          file: `${location}/${r.id}/${r.file}`,
                        },
                        fields: [
                          {
                            name: "passphrase",
                            label: "Vault passphrase",
                            type: "password",
                            required: true,
                          },
                        ],
                      })
                    }
                  >
                    Restore
                  </Button>
                </div>
              </Td>
            </tr>
          ))}
        </Table>
      </Section>
      <Section
        title="Schedules"
        description="When each server is backed up (local time) and how many backups it keeps. A tighter limit deletes the extra backups at once; the newest is never dropped for its age."
        actions={
          <Reload onClick={policies.reload} loading={policies.loading} />
        }
      >
        <Problem error={policies.error} />
        <Table head={["Server", "Schedule", "Keeps", "Next", "Backups", ""]}>
          {(policies.data ?? []).map((p) => (
            <tr key={p.instance}>
              <Td className="font-medium">{p.instance}</Td>
              <Td className={p.off ? "text-destructive" : undefined}>
                {describeSchedule(p)}
                {p.default && (
                  <span className="text-muted-foreground"> (default)</span>
                )}
              </Td>
              <Td>{describeKeep(p)}</Td>
              <Td className="text-muted-foreground">
                {p.next ? fmtDate(p.next) : "-"}
              </Td>
              <Td className="text-muted-foreground">{p.backups}</Td>
              <Td className="text-right">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    run({
                      action: "backup.schedule",
                      title: `Backups of ${p.instance}`,
                      confirmLabel: "Save",
                      description:
                        "Times are HH:MM in the server's local time, comma-separated. Days: every, or e.g. mon, thu. Keep: a number, all, or default (14). A tighter limit deletes the extra backups at once.",
                      params: { id: p.instance },
                      fields: [
                        {
                          name: "off",
                          label:
                            "Turn scheduled backups off (existing backups stay)",
                          type: "switch",
                          initial: p.off ? "true" : "false",
                        },
                        {
                          name: "times",
                          label: "Times",
                          initial: p.times.join(", "),
                          required: true,
                        },
                        {
                          name: "days",
                          label: "Days",
                          initial: p.days.length ? p.days.join(", ") : "every",
                          required: true,
                        },
                        {
                          name: "keep",
                          label: "Keep how many",
                          initial:
                            p.keepCount > 0 ? String(p.keepCount) : "all",
                          required: true,
                        },
                        {
                          name: "keepDays",
                          label:
                            "Also drop backups older than (days, 0 for no limit)",
                          type: "number",
                          initial: String(p.keepDays),
                        },
                      ],
                    })
                  }
                >
                  <CalendarClock /> Schedule
                </Button>
              </Td>
            </tr>
          ))}
        </Table>
      </Section>
      <Section
        title="Copies elsewhere"
        description="Backups are encrypted, so they're safe to copy off the server."
      >
        <p className="text-sm">
          For example, with rclone:{" "}
          <code className="bg-muted rounded px-1 font-mono text-xs">
            rclone sync {location} remote:sharkord-backups
          </code>
          . Choose where backups go on the Storage page.
        </p>
      </Section>
    </>
  );
}
