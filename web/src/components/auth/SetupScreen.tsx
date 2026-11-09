import { useState, type FormEvent } from "react";
import { TriangleAlert } from "lucide-react";
import { authApi, MIN_PASSWORD } from "@/authApi";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/lib/auth";
import { AuthLayout } from "./AuthLayout";
import { Field } from "./Field";
import { messageOf } from "@/lib/utils";
import { ErrorText } from "@/components/PageHeader";

export function SetupScreen() {
  const { adopt, setWelcome, status: authStatus } = useAuth();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [setupToken, setSetupToken] = useState("");
  const [tokenAsked, setTokenAsked] = useState(false);
  const askToken = tokenAsked || !!authStatus?.setupTokenRequired;
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const tooShort = password.length > 0 && password.length < MIN_PASSWORD;
  const mismatch = confirm.length > 0 && confirm !== password;
  const ready = username.trim() !== "" && password.length >= MIN_PASSWORD && confirm === password && (!askToken || setupToken.trim() !== "");

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!ready) return;
    setBusy(true);
    setError(null);
    try {
      const status = await authApi.setup(username.trim(), password, askToken ? setupToken.trim() : undefined);
      setWelcome(true);
      adopt(status);
    } catch (err) {
      // The address decides if the code is needed, and the answer is what counts if the page guessed wrong.
      if ((err as { status?: number }).status === 403) setTokenAsked(true);
      setError(messageOf(err, "Could not create the account"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthLayout
      title="Welcome. Let's set up your home."
      intro="Create the first account. It becomes the administrator: it controls every device and decides who else can sign in."
    >
      <form className="space-y-4" onSubmit={submit}>
        <Field label="Your name for signing in" autoComplete="username" autoFocus value={username} onChange={(e) => setUsername(e.target.value)} />
        <Field
          label="Password"
          type="password"
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          help={tooShort ? `Use at least ${MIN_PASSWORD} characters (${password.length} so far).` : `At least ${MIN_PASSWORD} characters. A few random words work well.`}
          aria-invalid={tooShort}
        />
        <Field
          label="Type the password again"
          type="password"
          autoComplete="new-password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          help={mismatch ? "These two don't match yet." : undefined}
          aria-invalid={mismatch}
        />
        {askToken && (
          <Field
            label="Setup code"
            autoComplete="off"
            value={setupToken}
            onChange={(e) => setSetupToken(e.target.value)}
            help="You're not on the computer running Home Control, so this proves you can reach it. The code is in the service's logs: run docker compose logs home-control there and look for setupCode."
          />
        )}
        <Alert variant="warning">
          <TriangleAlert aria-hidden />
          <AlertDescription className="text-sm">
            There is no email recovery. If this password is lost, nobody can reset it from the website. Write it down somewhere safe.
          </AlertDescription>
        </Alert>
        {error && <ErrorText>{error}</ErrorText>}
        <Button type="submit" className="w-full" disabled={!ready || busy}>{busy ? "Creating…" : "Create account and start"}</Button>
      </form>
    </AuthLayout>
  );
}
