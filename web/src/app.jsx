import { useCallback, useEffect, useState } from 'react'

import { api } from './api.js'
import { Browser } from './browser.jsx'
import { Survey } from './icons.jsx'
import { Shares, Tokens, Trash } from './panels.jsx'
import { SharePage } from './share.jsx'

// ponytail: a router in fifteen lines — pathname in, component out. react-router
// buys nothing here: there are six screens, no nested layouts, no loaders.
// Reach for it when a screen needs its own sub-routes.
function useRoute() {
  const [route, setRoute] = useState(() => window.location.pathname + window.location.search)
  useEffect(() => {
    const onPop = () => setRoute(window.location.pathname + window.location.search)
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])
  const navigate = useCallback((to, { replace = false } = {}) => {
    window.history[replace ? 'replaceState' : 'pushState']({}, '', to)
    setRoute(to)
  }, [])
  return [route, navigate]
}

export function App() {
  const [route, navigate] = useRoute()
  const [user, setUser] = useState(undefined) // undefined until /api/me has answered

  useEffect(() => {
    api('/api/me')
      .then(setUser)
      .catch(() => setUser(null))
  }, [])

  const path = route.split('?')[0]

  // A share link is the one screen with no session behind it, so it is decided
  // before anything asks who is signed in.
  if (path.startsWith('/s/')) return <SharePage token={decodeURIComponent(path.slice(3))} />
  if (path === '/setup') return <Setup navigate={navigate} onDone={setUser} />

  if (user === undefined) return <p className="centred">Opening the collection…</p>
  if (!user) return <Login onDone={setUser} />

  return <Shell user={user} route={route} navigate={navigate} onSignedOut={() => setUser(null)} />
}

// The four series of the collection. The roman numeral is the furniture a
// finding aid prints in its margin; the plain word beside it is what anyone
// actually reads, and it stays the word the rest of the interface uses.
const tabs = [
  ['/', 'I', 'Files'],
  ['/trash', 'II', 'Trash'],
  ['/shares', 'III', 'Shares'],
  ['/tokens', 'IV', 'API tokens'],
]

const isCurrent = (to, path) => (to === '/' ? path === '/' || path.startsWith('/browse') : path === to)

function Shell({ user, route, navigate, onSignedOut }) {
  const path = route.split('?')[0]
  const signOut = async () => {
    await api('/api/logout', { method: 'POST' }).catch(() => {})
    onSignedOut()
    navigate('/')
  }

  let screen = <Browser route={route} navigate={navigate} />
  if (path === '/trash') screen = <Trash />
  else if (path === '/shares') screen = <Shares navigate={navigate} />
  else if (path === '/tokens') screen = <Tokens />

  const at = tabs.findIndex(([to]) => isCurrent(to, path))

  return (
    <div className="shell">
      <header>
        <div className="masthead" role="banner">
          <span className="wordmark">drive</span>
          <span className="spacer" />
          <Indexing />
          <span className="who">{user.username}</span>
          <button onClick={signOut}>Sign out</button>
        </div>
        <nav className="series" aria-label="Series">
          <span className="series-mark" style={{ '--at': Math.max(0, at) }} aria-hidden="true" />
          {tabs.map(([to, numeral, label]) => {
            const on = isCurrent(to, path)
            return (
              <a
                key={to}
                href={to}
                className={on ? 'on' : ''}
                aria-current={on ? 'page' : undefined}
                onClick={(e) => {
                  e.preventDefault()
                  navigate(to)
                }}
              >
                <span className="numeral" aria-hidden="true">
                  {numeral}
                </span>
                <span className="label">{label}</span>
              </a>
            )
          })}
        </nav>
      </header>
      {screen}
    </div>
  )
}

// 13.12: while a scan is running a listing is a partial answer, and saying so is
// the difference between "you have no files" and "not indexed yet". It polls
// only while indexing, so a settled instance makes no requests at all.
//
// This is the whole collection being surveyed, which is why it sits in the
// masthead rather than in one screen's notes: it is true of every listing on
// screen, not just the folder in front of you.
function Indexing() {
  const [status, setStatus] = useState(null)
  useEffect(() => {
    let live = true
    let timer
    const poll = async () => {
      const next = await api('/api/scan').catch(() => null)
      if (!live) return
      setStatus(next)
      if (next?.indexing) timer = setTimeout(poll, 2000)
    }
    poll()
    return () => {
      live = false
      clearTimeout(timer)
    }
  }, [])

  if (!status?.indexing) return null
  return (
    <span className="indexing" title="Listings and search may be incomplete until this finishes">
      <Survey />
      Indexing {status.seen.toLocaleString()}
      {status.root ? ` · ${status.root}` : ''}
    </span>
  )
}

function Credentials({ title, action, error, busy, onSubmit, children }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  return (
    <div className="gate">
      <form
        className="card"
        onSubmit={(e) => {
          e.preventDefault()
          onSubmit(username, password)
        }}
      >
        <h1>{title}</h1>
        {children}
        <label>
          Username
          <input value={username} onChange={(e) => setUsername(e.target.value)} autoFocus autoComplete="username" />
        </label>
        <label>
          Password
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
          />
        </label>
        {error && <p className="error">{error}</p>}
        <button className="primary" disabled={busy}>
          {busy ? 'Working…' : action}
        </button>
      </form>
    </div>
  )
}

function Login({ onDone }) {
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const submit = async (username, password) => {
    setBusy(true)
    setError('')
    try {
      onDone(await api('/api/login', { method: 'POST', body: { username, password } }))
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }
  return <Credentials title="Sign in" action="Sign in" error={error} busy={busy} onSubmit={submit} />
}

// 13.2: first run. The token comes from the URL the server printed to its own
// output; without a valid one this screen refuses to render a form at all.
function Setup({ navigate, onDone }) {
  const token = new URLSearchParams(window.location.search).get('token') || ''
  const [open, setOpen] = useState(undefined)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api(`/api/setup?token=${encodeURIComponent(token)}`)
      .then(() => setOpen(true))
      .catch((err) => {
        setOpen(false)
        setError(err.message)
      })
  }, [token])

  const submit = async (username, password) => {
    setBusy(true)
    setError('')
    try {
      const user = await api('/api/setup', { method: 'POST', body: { username, password, token } })
      onDone(user)
      // Setup logs the operator straight in, so land them in their own drive
      // rather than back on a form that no longer works — and through navigate,
      // because a bare replaceState changes the URL without telling the router.
      navigate('/', { replace: true })
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  if (open === undefined) return <p className="centred">Checking the setup link…</p>
  if (!open) {
    return (
      <div className="gate">
        <div className="card">
          <h1>Setup is closed</h1>
          <p className="error">{error}</p>
          <p>
            If this drive has no administrator yet, restart it and use the link it prints. Otherwise{' '}
            <a href="/">sign in</a>.
          </p>
        </div>
      </div>
    )
  }
  return (
    <Credentials title="Create the administrator" action="Create account" error={error} busy={busy} onSubmit={submit}>
      <p>This is the only account that can create others. There is no default password.</p>
    </Credentials>
  )
}
