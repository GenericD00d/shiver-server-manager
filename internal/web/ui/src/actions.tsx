import { CircleCheck, CircleX, LoaderCircle } from 'lucide-react';
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react';
import { api, ApiError, type Job } from './api';
import { Button } from './components/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './components/dialog';
import { Input } from './components/input';
import { Label } from './components/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from './components/select';
import { Switch } from './components/switch';

// A field in an action's form.
export type Field = {
  name: string;
  label: string;
  type?: 'text' | 'password' | 'number' | 'select' | 'switch' | 'file';
  accept?: string; // file fields: the types offered
  options?: { value: string; label: string }[];
  help?: string;
  placeholder?: string;
  required?: boolean;
  initial?: string;
};

// An action the page can run: a form, an optional warning, then the same
// command the terminal would run, with its output shown live.
export type ActionSpec = {
  action: string;
  title: string;
  description?: ReactNode;
  fields?: Field[];
  params?: Record<string, string>; // fixed inputs
  destructive?: boolean;
  confirmLabel?: string;
};

type Ctx = { run: (spec: ActionSpec) => void; reauth: Record<string, boolean>; jobsChanged: number };
const ActionCtx = createContext<Ctx>({ run: () => {}, reauth: {}, jobsChanged: 0 });
export const useActions = () => useContext(ActionCtx);

export function ActionProvider({ reauth, children }: { reauth: Record<string, boolean>; children: ReactNode }) {
  const [spec, setSpec] = useState<ActionSpec | null>(null);
  const [jobsChanged, setJobsChanged] = useState(0);
  return (
    <ActionCtx.Provider value={{ run: setSpec, reauth, jobsChanged }}>
      {children}
      {spec && (
        <ActionDialog
          key={spec.action + JSON.stringify(spec.params)}
          spec={spec}
          needsPassword={!!reauth[spec.action]}
          onClose={() => setSpec(null)}
          onFinished={() => setJobsChanged((n) => n + 1)}
        />
      )}
    </ActionCtx.Provider>
  );
}

// upload sends a file to the back end, which keeps it for the action and
// returns its path on the server.
async function upload(file: File): Promise<string> {
  const res = await fetch(`/api/upload?name=${encodeURIComponent(file.name)}`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/octet-stream', 'X-Requested-By': 'shiver-server-manager' },
    body: file
  });
  const data = (await res.json().catch(() => ({}))) as { path?: string; error?: string };
  if (!res.ok || !data.path) throw new ApiError(res.status, { error: data.error ?? `upload failed (${res.status})` });
  return data.path;
}

function FieldInput({ f, value, onChange, onFile }: { f: Field; value: string; onChange: (v: string) => void; onFile?: (file: File | null) => void }) {
  const id = `field-${f.name}`;
  if (f.type === 'file') {
    return (
      <div className="grid gap-1.5">
        <Label htmlFor={id}>{f.label}</Label>
        <Input
          id={id}
          type="file"
          accept={f.accept}
          onChange={(e) => {
            const file = e.target.files?.[0] ?? null;
            onFile?.(file);
            onChange(file?.name ?? '');
          }}
        />
        {f.help && <p className="text-muted-foreground text-xs">{f.help}</p>}
      </div>
    );
  }
  if (f.type === 'switch') {
    return (
      <div className="flex items-center justify-between gap-4">
        <div>
          <Label htmlFor={id}>{f.label}</Label>
          {f.help && <p className="text-muted-foreground text-xs mt-1">{f.help}</p>}
        </div>
        <Switch id={id} checked={value === 'true'} onCheckedChange={(c) => onChange(c ? 'true' : 'false')} />
      </div>
    );
  }
  return (
    <div className="grid gap-1.5">
      <Label htmlFor={id}>{f.label}</Label>
      {f.type === 'select' ? (
        <Select value={value} onValueChange={onChange}>
          <SelectTrigger id={id} className="w-full">
            <SelectValue placeholder={f.placeholder ?? 'Choose…'} />
          </SelectTrigger>
          <SelectContent>
            {f.options?.map((o) => (
              <SelectItem key={o.value} value={o.value}>
                {o.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        <Input
          id={id}
          type={f.type === 'password' ? 'password' : f.type === 'number' ? 'number' : 'text'}
          autoComplete={f.type === 'password' ? 'off' : undefined}
          value={value}
          placeholder={f.placeholder}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
      {f.help && <p className="text-muted-foreground text-xs">{f.help}</p>}
    </div>
  );
}

function ActionDialog({ spec, needsPassword, onClose, onFinished }: { spec: ActionSpec; needsPassword: boolean; onClose: () => void; onFinished: () => void }) {
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries((spec.fields ?? []).map((f) => [f.name, f.initial ?? (f.type === 'switch' ? 'false' : '')]))
  );
  const [password, setPassword] = useState('');
  const [files, setFiles] = useState<Record<string, File | null>>({});
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [job, setJob] = useState<Job | null>(null);

  const missing = (spec.fields ?? []).some((f) => f.required && !values[f.name]) || (needsPassword && !password);

  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      const uploaded: Record<string, string> = {};
      for (const f of spec.fields ?? []) {
        const file = files[f.name];
        if (f.type === 'file' && file) uploaded[f.name] = await upload(file);
      }
      const j = await api.post<Job>(`/api/actions/${spec.action}`, {
        params: { ...values, ...uploaded, ...spec.params },
        password: needsPassword ? password : undefined
      });
      setJob(j);
    } catch (e) {
      setError((e as ApiError).message);
    } finally {
      setBusy(false);
      setPassword('');
    }
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{spec.title}</DialogTitle>
          {spec.description && !job && <DialogDescription asChild><div>{spec.description}</div></DialogDescription>}
        </DialogHeader>
        {job ? (
          <JobOutput job={job} onDone={onFinished} />
        ) : (
          <form
            className="grid gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              if (!missing) submit();
            }}
          >
            {(spec.fields ?? []).map((f) => (
              <FieldInput
                key={f.name}
                f={f}
                value={values[f.name] ?? ''}
                onChange={(v) => setValues((s) => ({ ...s, [f.name]: v }))}
                onFile={(file) => setFiles((s) => ({ ...s, [f.name]: file }))}
              />
            ))}
            {needsPassword && (
              <FieldInput
                f={{ name: 'password', label: 'Your password, again', type: 'password', help: 'This action asks for your OS password again.' }}
                value={password}
                onChange={setPassword}
              />
            )}
            {error && <p className="text-destructive text-sm">{error}</p>}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={onClose}>
                Cancel
              </Button>
              <Button type="submit" variant={spec.destructive ? 'destructive' : 'default'} disabled={busy || missing}>
                {busy && <LoaderCircle className="animate-spin" />}
                {spec.confirmLabel ?? spec.title}
              </Button>
            </DialogFooter>
          </form>
        )}
        {job && (
          <DialogFooter>
            <Button onClick={onClose}>Close</Button>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  );
}

// JobOutput streams a running command's progress lines.
export function JobOutput({ job, onDone }: { job: Job; onDone?: () => void }) {
  const [lines, setLines] = useState<string[]>([]);
  const [exit, setExit] = useState<number | null>(job.done ? job.exitCode : null);
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const es = new EventSource(`/api/jobs/${job.id}/events`);
    es.addEventListener('line', (e) => setLines((l) => [...l, JSON.parse((e as MessageEvent).data)]));
    es.addEventListener('done', (e) => {
      setExit(Number((e as MessageEvent).data));
      es.close();
      onDone?.();
    });
    es.onerror = () => es.close();
    return () => es.close();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [job.id]);

  useEffect(() => {
    box.current?.scrollTo({ top: box.current.scrollHeight });
  }, [lines]);

  return (
    <div className="grid gap-3">
      <div ref={box} className="bg-muted max-h-80 min-h-24 overflow-auto rounded-md p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap">
        {lines.length === 0 && exit === null ? <span className="text-muted-foreground">Starting…</span> : lines.join('\n')}
      </div>
      <div className="flex items-center gap-2 text-sm">
        {exit === null ? (
          <>
            <LoaderCircle className="size-4 animate-spin" /> Running…
          </>
        ) : exit === 0 ? (
          <>
            <CircleCheck className="size-4" /> Done.
          </>
        ) : (
          <>
            <CircleX className="text-destructive size-4" />
            <span>
              Didn't finish (exit code {exit}).{exit === 5 && ' Its progress is saved: System → Repair carries on.'}
            </span>
          </>
        )}
      </div>
    </div>
  );
}
