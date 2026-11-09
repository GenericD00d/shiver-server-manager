import { LoaderCircle } from 'lucide-react';
import { useState } from 'react';
import { api, ApiError, type Me } from './api';
import { Button } from './components/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './components/card';
import { Input } from './components/input';
import { Label } from './components/label';

export function Login({ onLoggedIn }: { onLoggedIn: (me: Me) => void }) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [codePrompt, setCodePrompt] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      await api.post('/api/login', { username, password, code: codePrompt ? code : undefined });
      onLoggedIn(await api.get<Me>('/api/me'));
    } catch (e) {
      const err = e as ApiError;
      if (err.body?.needCode) {
        setCodePrompt(String(err.body.prompt || 'Code'));
        if (codePrompt) setError('That code was wrong.');
      } else {
        setError(err.message);
        setCode('');
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex min-h-svh items-center justify-center p-4">
      <Card className="w-full max-w-sm">
        <CardHeader className="items-center text-center">
          <img src="/logo.png" alt="" className="mx-auto mb-2 size-20 rounded-full" />
          <CardTitle>Shiver Server Manager</CardTitle>
          <CardDescription>Log in with your server account. Only members of shiver-admins can.</CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className="grid gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              submit();
            }}
          >
            <div className="grid gap-1.5">
              <Label htmlFor="username">Username</Label>
              <Input id="username" autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} autoFocus />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="password">Password</Label>
              <Input id="password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
            </div>
            {codePrompt && (
              <div className="grid gap-1.5">
                <Label htmlFor="code">{codePrompt.replace(/:\s*$/, '')}</Label>
                <Input id="code" autoComplete="one-time-code" inputMode="numeric" value={code} onChange={(e) => setCode(e.target.value)} autoFocus />
              </div>
            )}
            {error && <p className="text-destructive text-sm">{error}</p>}
            <Button type="submit" disabled={busy || !username || !password || (!!codePrompt && !code)}>
              {busy && <LoaderCircle className="animate-spin" />}
              Log in
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
