import { useEffect, useState } from 'react'
import { BellRing, Mail, Send } from 'lucide-react'
import { api, type NotifySettings, type NotifySettingsUpdate } from '../api'
import { Alert } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card'
import { Input } from '../components/ui/input'

// Notification settings. The env (git-managed) config is the baseline; every
// field here is an override stored in the app database. Blank field = use the
// git value. Secrets are write-only: the API only reports whether one is set.

type SecretState = { value: string; touched: boolean }

export default function Settings() {
  const [settings, setSettings] = useState<NotifySettings | null>(null)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)
  const [busy, setBusy] = useState(false)
  const [testResult, setTestResult] = useState<Record<string, string> | null>(null)
  const [testBusy, setTestBusy] = useState(false)

  // ntfy overrides
  const [ntfyServer, setNtfyServer] = useState('')
  const [ntfyTopic, setNtfyTopic] = useState('')
  const [ntfyToken, setNtfyToken] = useState<SecretState>({ value: '', touched: false })
  const [ntfyDisabled, setNtfyDisabled] = useState(false)
  // email overrides
  const [smtpHost, setSmtpHost] = useState('')
  const [smtpPort, setSmtpPort] = useState('')
  const [smtpTls, setSmtpTls] = useState('')
  const [smtpUser, setSmtpUser] = useState('')
  const [smtpPass, setSmtpPass] = useState<SecretState>({ value: '', touched: false })
  const [smtpFrom, setSmtpFrom] = useState('')
  const [smtpTo, setSmtpTo] = useState('')
  const [emailDisabled, setEmailDisabled] = useState(false)

  function hydrate(s: NotifySettings) {
    setSettings(s)
    setNtfyServer(s.overrides.ntfyServer)
    setNtfyTopic(s.overrides.ntfyTopic)
    setNtfyToken({ value: '', touched: false })
    setNtfyDisabled(s.overrides.ntfyDisabled)
    setSmtpHost(s.overrides.smtpHost)
    setSmtpPort(s.overrides.smtpPort ? String(s.overrides.smtpPort) : '')
    setSmtpTls(s.overrides.smtpTls)
    setSmtpUser(s.overrides.smtpUser)
    setSmtpPass({ value: '', touched: false })
    setSmtpFrom(s.overrides.smtpFrom)
    setSmtpTo(s.overrides.smtpTo)
    setEmailDisabled(s.overrides.emailDisabled)
  }

  useEffect(() => {
    api.notifySettings().then(hydrate).catch((e: Error) => setError(e.message))
  }, [])

  async function save() {
    setBusy(true)
    setError('')
    setSaved(false)
    try {
      const body: NotifySettingsUpdate = {
        ntfyServer,
        ntfyTopic,
        ntfyDisabled,
        smtpHost,
        smtpPort: smtpPort ? Number(smtpPort) : 0,
        smtpTls,
        smtpUser,
        smtpFrom,
        smtpTo,
        emailDisabled,
      }
      if (ntfyToken.touched) body.ntfyToken = ntfyToken.value
      if (smtpPass.touched) body.smtpPass = smtpPass.value
      const next = await api.updateNotifySettings(body)
      hydrate(next)
      setSaved(true)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function test() {
    setTestBusy(true)
    setTestResult(null)
    try {
      const res = await api.notifyTest()
      setTestResult(res.channels)
    } catch (e) {
      setTestResult({ error: (e as Error).message })
    } finally {
      setTestBusy(false)
    }
  }

  const env = settings?.env

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
        <p className="text-sm text-muted-foreground">
          Notification config from git (env) is the baseline — anything set here overrides it.
          Blank fields fall back to the git value.
        </p>
      </div>

      {error && <Alert variant="danger">{error}</Alert>}
      {saved && <Alert>Settings saved and applied — no restart needed.</Alert>}

      <Card>
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 space-y-0">
          <div>
            <CardTitle>Active channels</CardTitle>
            <CardDescription>Effective after env + overrides are merged</CardDescription>
          </div>
          <div className="flex items-center gap-2">
            {settings?.channels.length ? (
              settings.channels.map((c) => (
                <Badge key={c} variant="success">
                  {c}
                </Badge>
              ))
            ) : (
              <Badge variant="secondary">none configured</Badge>
            )}
            <Button size="sm" variant="outline" onClick={test} disabled={testBusy || !settings?.channels.length}>
              <Send className="h-3.5 w-3.5" />
              {testBusy ? 'Sending…' : 'Send test'}
            </Button>
          </div>
        </CardHeader>
        {testResult && (
          <CardContent className="border-t pt-4 text-sm">
            {Object.entries(testResult).map(([ch, res]) => (
              <div key={ch} className="flex items-center gap-2">
                <Badge variant={res === 'ok' ? 'success' : 'danger'}>{ch}</Badge>
                <span className={res === 'ok' ? 'text-muted-foreground' : 'text-destructive'}>{res}</span>
              </div>
            ))}
          </CardContent>
        )}
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <BellRing className="h-4 w-4" /> ntfy
          </CardTitle>
          <CardDescription>
            Enabled when a topic is set (git: {env?.ntfyTopic ? <code className="font-mono">{env.ntfyTopic}</code> : 'not set'})
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 sm:grid-cols-2">
          <Field label="Server" env={env?.ntfyServer} value={ntfyServer} onChange={setNtfyServer} />
          <Field label="Topic" env={env?.ntfyTopic} value={ntfyTopic} onChange={setNtfyTopic} />
          <SecretField
            label="Access token"
            envSet={!!env?.ntfyTokenSet}
            overrideSet={!!settings?.overrides.ntfyTokenSet}
            state={ntfyToken}
            onChange={setNtfyToken}
          />
          <label className="flex items-center gap-2 self-end pb-2 text-sm">
            <input
              type="checkbox"
              checked={ntfyDisabled}
              onChange={(e) => setNtfyDisabled(e.target.checked)}
            />
            Disable ntfy (even if configured in git)
          </label>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Mail className="h-4 w-4" /> Email (SMTP)
          </CardTitle>
          <CardDescription>
            Enabled when host, from, and to are all set (git host:{' '}
            {env?.smtpHost ? <code className="font-mono">{env.smtpHost}</code> : 'not set'})
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 sm:grid-cols-2">
          <Field label="Host" env={env?.smtpHost} value={smtpHost} onChange={setSmtpHost} />
          <Field
            label="Port"
            env={env?.smtpPort ? String(env.smtpPort) : ''}
            value={smtpPort}
            onChange={(v) => setSmtpPort(v.replace(/[^0-9]/g, ''))}
          />
          <div className="grid gap-1.5">
            <label className="text-xs text-muted-foreground">
              TLS mode {env?.smtpTls ? `(git: ${env.smtpTls})` : ''}
            </label>
            <select
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
              value={smtpTls}
              onChange={(e) => setSmtpTls(e.target.value)}
            >
              <option value="">use git value</option>
              <option value="starttls">starttls</option>
              <option value="tls">tls (implicit)</option>
              <option value="none">none</option>
            </select>
          </div>
          <Field label="Username" env={env?.smtpUser} value={smtpUser} onChange={setSmtpUser} />
          <SecretField
            label="Password"
            envSet={!!env?.smtpPassSet}
            overrideSet={!!settings?.overrides.smtpPassSet}
            state={smtpPass}
            onChange={setSmtpPass}
          />
          <Field label="From" env={env?.smtpFrom} value={smtpFrom} onChange={setSmtpFrom} />
          <Field
            label="To (comma-separated)"
            env={env?.smtpTo}
            value={smtpTo}
            onChange={setSmtpTo}
          />
          <label className="flex items-center gap-2 self-end pb-2 text-sm">
            <input
              type="checkbox"
              checked={emailDisabled}
              onChange={(e) => setEmailDisabled(e.target.checked)}
            />
            Disable email (even if configured in git)
          </label>
        </CardContent>
      </Card>

      <div className="flex justify-end">
        <Button onClick={save} disabled={busy || !settings}>
          {busy ? 'Saving…' : 'Save & apply'}
        </Button>
      </div>
    </div>
  )
}

function Field({
  label,
  env,
  value,
  onChange,
}: {
  label: string
  env?: string
  value: string
  onChange: (v: string) => void
}) {
  return (
    <div className="grid gap-1.5">
      <label className="text-xs text-muted-foreground">{label}</label>
      <Input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={env ? `git: ${env}` : 'not set in git'}
      />
    </div>
  )
}

function SecretField({
  label,
  envSet,
  overrideSet,
  state,
  onChange,
}: {
  label: string
  envSet: boolean
  overrideSet: boolean
  state: SecretState
  onChange: (s: SecretState) => void
}) {
  const status = overrideSet ? 'override set' : envSet ? 'set in git' : 'not set'
  return (
    <div className="grid gap-1.5">
      <label className="text-xs text-muted-foreground">
        {label} <span className="opacity-70">({status})</span>
      </label>
      <div className="flex gap-2">
        <Input
          type="password"
          value={state.value}
          onChange={(e) => onChange({ value: e.target.value, touched: true })}
          placeholder={overrideSet || envSet ? 'leave blank to keep current' : 'enter to set'}
          autoComplete="new-password"
        />
        {overrideSet && (
          <Button
            type="button"
            size="sm"
            variant="outline"
            title="Remove the override and fall back to the git value"
            onClick={() => onChange({ value: '', touched: true })}
          >
            Clear
          </Button>
        )}
      </div>
      {state.touched && state.value === '' && overrideSet && (
        <p className="text-[11px] text-muted-foreground">Override will be removed on save.</p>
      )}
    </div>
  )
}
