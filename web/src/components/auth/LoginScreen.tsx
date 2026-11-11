import { useState, type FormEvent } from "react";
import { authApi } from "@/authApi";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/lib/auth";
import { AuthLayout } from "./AuthLayout";
import { Field } from "./Field";
import { messageOf } from "@/lib/utils";
import { ErrorText } from "@/components/PageHeader";

export function LoginScreen({ overlay = false }: { overlay?: boolean }) {
  const { adopt } = useAuth();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const status = await authApi.login(username.trim(), password);
      setPassword("");
      adopt(status);
    } catch (err) {
      const status = (err as { status?: number }).status;
      setError(
        status === 429 ? "Too many attempts, try again shortly"
          : status === 401 ? "That name or password isn't right. Check both and try again."
            : messageOf(err, "Could not sign in"),
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthLayout
      overlay={overlay}
      title={overlay ? "Sign in again to continue" : "Welcome back"}
      intro={overlay ? "Your session ended. Sign in and you'll be right back where you were; nothing you typed is lost." : "Sign in to control your home."}
    >
      <form className="space-y-4" onSubmit={submit}>
        <Field label="Name" autoComplete="username" autoFocus value={username} onChange={(e) => setUsername(e.target.value)} />
        <Field label="Password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        {error && <ErrorText>{error}</ErrorText>}
        <Button type="submit" className="w-full" disabled={busy || !username.trim() || !password}>{busy ? "Signing in…" : "Sign in"}</Button>
        <p className="text-center text-xs text-muted-foreground">Forgot your password? Ask the person who set up this home, an administrator can reset it.</p>
      </form>
    </AuthLayout>
  );
}
