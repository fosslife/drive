import { useCallback, useEffect, useState } from 'react'

import { api } from './api.js'
import { formatDate, formatSize } from './format.js'
import { Extent, Panel, useCollection } from './panels.jsx'

// What an account is holding, said the way the rest of the interface says
// extent. An account with no quota gets a figure and no limit, which is the
// honest way to print "unlimited" beside a number.
const holding = (account) => {
  const used = formatSize(account.usage_bytes || 0)
  return account.quota_bytes ? `${used} of ${formatSize(account.quota_bytes)}` : used
}

// bytesFrom reads what an administrator typed into the limit prompt. Plain
// bytes, because that is what the API takes and what the figure beside it is
// in; zero is the no-limit answer.
const bytesFrom = (answer) => {
  const n = Number(String(answer).trim())
  return Number.isFinite(n) && n >= 0 ? Math.round(n) : null
}

// The accounts on this instance and the state of the instance itself. One
// screen because they are one job: the operator looking after other people's
// drives without a terminal.
export function Accounts() {
  const { items, error, loading, act } = useCollection('/api/admin/users')
  const [adding, setAdding] = useState(false)
  const instance = useInstance()

  const patch = (account, body) => act(() => api(`/api/admin/users/${account.username}`, { method: 'PATCH', body }))

  const setLimit = (account) => {
    const answer = window.prompt(
      `Storage limit for ${account.username}, in bytes. 0 is no limit.`,
      String(account.quota_bytes || 0),
    )
    if (answer === null) return
    const bytes = bytesFrom(answer)
    if (bytes === null) {
      window.alert('A storage limit is a whole number of bytes.')
      return
    }
    patch(account, { quota_bytes: bytes })
  }

  const resetPassword = (account) => {
    const password = window.prompt(`New password for ${account.username}. They are not asked for the old one.`)
    if (!password) return
    act(() => api(`/api/admin/users/${account.username}/password`, { method: 'POST', body: { password } }))
  }

  const remove = (account) => {
    const warning = `Delete the account ${account.username}? Their files stay on disk — re-creating the account with the same name gives them back.`
    if (window.confirm(warning)) {
      act(() => api(`/api/admin/users/${account.username}`, { method: 'DELETE' }))
    }
  }

  const create = async (event) => {
    event.preventDefault()
    const form = new FormData(event.target)
    await act(() =>
      api('/api/admin/users', {
        method: 'POST',
        body: { username: form.get('username'), password: form.get('password') },
      }),
    )
    setAdding(false)
  }

  const disabled = items.filter((a) => a.disabled).length

  return (
    <Panel
      title="Accounts"
      error={error}
      loading={loading}
      actions={
        <>
          <button onClick={instance.rescan}>Rescan</button>
          <button className="primary" onClick={() => setAdding((on) => !on)}>
            New account
          </button>
        </>
      }
      reveal={
        adding && (
          <form className="card" onSubmit={create}>
            <h1>Create an account</h1>
            <p>They get a drive of their own. Nobody, you included, can read another account's files.</p>
            <label>
              Username
              <input name="username" autoFocus autoComplete="off" />
            </label>
            <label>
              Password
              <input name="password" type="password" autoComplete="new-password" />
            </label>
            <div className="row-actions">
              <button className="primary">Create account</button>
              <button type="button" onClick={() => setAdding(false)}>
                Cancel
              </button>
            </div>
          </form>
        )
      }
      notes={
        <>
          <Extent count={items.length} unit="account" sub={disabled ? `${disabled} disabled` : null} />
          <InstanceNotes state={instance.state} />
          <section className="prose">
            <h2>Access and use</h2>
            <p>
              The people who hold this drive. Each account has its own files at its own paths, and administering an
              account never opens what is inside it.
            </p>
            <p className="caveat">
              Deleting an account removes the login, not the files. A storage limit applies to the next upload only — it
              never deletes or hides anything already there, and it cannot hold back files added on the server itself.
            </p>
          </section>
        </>
      }
    >
      <table>
        <thead>
          <tr>
            <th>Account</th>
            <th>State</th>
            <th className="num">Holding</th>
            <th className="num">Items</th>
            <th className="num">Since</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {items.map((account) => (
            <tr key={account.id} className={account.disabled ? 'stale' : ''}>
              <td>
                {account.username}
                {account.is_admin && <em> — administrator</em>}
              </td>
              <td>{account.disabled ? 'Disabled' : 'Active'}</td>
              <td className="num">{holding(account)}</td>
              <td className="num">{(account.file_count || 0).toLocaleString()}</td>
              <td className="num">{formatDate(account.created_at)}</td>
              <td className="actions">
                <button onClick={() => patch(account, { disabled: !account.disabled })}>
                  {account.disabled ? 'Enable' : 'Disable'}
                </button>
                <button onClick={() => setLimit(account)}>Limit</button>
                <button onClick={() => resetPassword(account)}>Reset password</button>
                <button className="destructive" onClick={() => remove(account)}>
                  Delete
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </Panel>
  )
}

// useInstance keeps the instance figures beside the accounts and reloads them
// when a scan is asked for, so pressing the button has a visible effect.
function useInstance() {
  const [state, setState] = useState(null)

  const reload = useCallback(async () => {
    setState(await api('/api/admin/instance').catch(() => null))
  }, [])

  useEffect(() => {
    reload()
  }, [reload])

  const rescan = async () => {
    await api('/api/admin/scan', { method: 'POST' }).catch(() => {})
    reload()
  }
  return { state, rescan }
}

// What the operator would otherwise need a shell to find out. It sits in the
// notes column because that is where this interface puts everything it has to
// say about what is on screen.
function InstanceNotes({ state }) {
  if (!state) return null
  const scan = state.scan || {}
  return (
    <section className="prose">
      <h2>This drive</h2>
      <p className="live">
        {formatSize(state.free_bytes)} free of {formatSize(state.total_bytes)}
      </p>
      <p>
        Version {state.version}, running {Math.max(1, Math.round(state.uptime_seconds / 60)).toLocaleString()} min, with
        its files in {state.data_dir}
      </p>
      <p>{indexingLine(scan)}</p>
      {scan.error && <p className="caveat">The last indexing run failed: {scan.error}</p>}
    </section>
  )
}

const indexingLine = (scan) => {
  if (scan.running) return `Indexing now — ${(scan.seen || 0).toLocaleString()} entries so far`
  if (!scan.scans) return 'Not indexed yet'
  const took = ((scan.took_ms || 0) / 1000).toFixed(1)
  return `Last indexed ${formatDate(scan.finished)}, ${(scan.seen || 0).toLocaleString()} entries in ${took}s`
}

// The reader's own card. It is reached from the masthead rather than the series
// navigation because it is not part of the collection: it is about the person
// looking at it.
export function AccountCard({ user }) {
  const [state, setState] = useState(user)
  const [error, setError] = useState('')
  const [done, setDone] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api('/api/me')
      .then(setState)
      .catch(() => {})
  }, [])

  const submit = async (event) => {
    event.preventDefault()
    const form = event.target
    setBusy(true)
    setDone('')
    try {
      await api('/api/me/password', {
        method: 'POST',
        body: { current_password: form.current_password.value, new_password: form.new_password.value },
      })
      // Cleared only once it worked. A wrong current password leaves what was
      // typed where it was typed, next to the reason it was refused.
      form.reset()
      setError('')
      setDone('Your password is changed. Any other browser you left signed in has been signed out.')
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Panel
      title="Your account"
      error={error}
      loading={false}
      notes={
        <>
          <Extent count={state.file_count || 0} unit="item" sub={holding(state)} />
          <section className="prose">
            <h2>Access and use</h2>
            <p>
              Signed in as <strong>{state.username}</strong>
              {state.is_admin ? ', the administrator of this drive.' : '.'}
            </p>
            <p className="caveat">
              Changing your password signs out every other browser you left signed in. API tokens keep working; revoke
              those from API tokens.
            </p>
          </section>
        </>
      }
    >
      <form className="card" onSubmit={submit}>
        <h1>Change your password</h1>
        <label>
          Current password
          <input name="current_password" type="password" autoComplete="current-password" />
        </label>
        <label>
          New password
          <input name="new_password" type="password" autoComplete="new-password" />
        </label>
        {done && <p className="note">{done}</p>}
        <div className="row-actions">
          <button className="primary" disabled={busy}>
            {busy ? 'Working…' : 'Change password'}
          </button>
        </div>
      </form>
    </Panel>
  )
}
