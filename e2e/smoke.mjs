// Plays the platform end to end over the real path: Telegram login -> Centrifugo WebSocket -> RPC
// proxy -> Go -> ledger driver, then a brand-signed JWT linked to the same account. No dependencies:
// Centrifugo's JSON protocol over Node's WebSocket.
//   API=http://127.0.0.1:18080 WS=ws://127.0.0.1:18000/connection/websocket BOT=123:test JWT_SECRET=… node e2e/smoke.mjs
import assert from 'node:assert/strict'
import { createHmac, randomUUID } from 'node:crypto'

const { API, WS, BOT, JWT_SECRET } = process.env

// jwt is what a brand's backend signs for its user sub (the custom auth provider).
function jwt(sub) {
  const enc = (o) => Buffer.from(JSON.stringify(o)).toString('base64url')
  const body = `${enc({ alg: 'HS256', typ: 'JWT' })}.${enc({ sub, aud: 'gamify-e2e', exp: Math.floor(Date.now() / 1000) + 300 })}`
  return `${body}.${createHmac('sha256', JWT_SECRET).update(body).digest('base64url')}`
}

async function loginWith(provider, credential) {
  const r = await fetch(`${API}/auth/${provider}`, { method: 'POST', body: JSON.stringify(credential) })
  assert.equal(r.status, 200, `login ${provider}`)
  return connect((await r.json()).token)
}

function initData(tgID) {
  const fields = { auth_date: String(Math.floor(Date.now() / 1000)), user: JSON.stringify({ id: tgID, first_name: 'Smoke' }) }
  const check = Object.keys(fields).sort().map((k) => `${k}=${fields[k]}`).join('\n')
  const secret = createHmac('sha256', 'WebAppData').update(BOT).digest()
  return new URLSearchParams({ ...fields, hash: createHmac('sha256', secret).update(check).digest('hex') }).toString()
}

async function connect(token) {
  const ws = new WebSocket(WS)
  const waiting = new Map()
  const pushes = []
  let id = 0
  ws.onmessage = (e) => {
    for (const line of String(e.data).split('\n').filter(Boolean)) {
      const m = JSON.parse(line)
      if (m.id && waiting.has(m.id)) {
        waiting.get(m.id)(m)
        waiting.delete(m.id)
      } else if (m.push) pushes.push(m.push)
      else if (!m.id) ws.send('{}') // ping -> pong
    }
  }
  await new Promise((ok, fail) => ((ws.onopen = ok), (ws.onerror = fail)))
  const send = (cmd) => new Promise((ok) => { const n = ++id; waiting.set(n, ok); ws.send(JSON.stringify({ id: n, ...cmd })) })
  const hello = await send({ connect: { token } })
  assert.ok(hello.connect, `connect: ${JSON.stringify(hello)}`)
  return {
    pushes,
    close: () => ws.close(),
    async rpc(method, data = {}) {
      const r = await send({ rpc: { method, data } })
      if (r.error) return { error: r.error }
      return r.rpc.data ?? {}
    },
  }
}

const tg = 1e9 + Math.floor(Math.random() * 1e9)
const c = await loginWith('telegram', { init_data: initData(tg) })

assert.deepEqual(await c.rpc('wallet.balances'), { usd: 0, tickets: 1000 }, 'welcome grant')
assert.equal((await c.rpc('nope.nope')).error.code, 1001, 'unknown method')
assert.equal((await c.rpc('dice.bet', { amount: 100, target: 5000, over: true, idem_key: 'broke' })).error.code, 1101, 'no money')
assert.equal((await c.rpc('wallet.faucet')).usd, 10000, 'faucet')

let usd = 10000
const bets = []
for (let i = 0; i < 20; i++) {
  const r = await c.rpc('dice.bet', { amount: 100, target: 5000, over: true, idem_key: randomUUID() })
  assert.ok(!r.error, JSON.stringify(r))
  usd += r.payout - r.amount
  assert.equal(r.balances.usd, usd, `bet ${i} balance`)
  bets.push(r)
}
const key = randomUUID()
const once = await c.rpc('dice.bet', { amount: 100, target: 5000, over: true, idem_key: key })
const again = await c.rpc('dice.bet', { amount: 100, target: 5000, over: true, idem_key: key })
assert.equal(again.bet_id, once.bet_id, 'idempotent')
usd += once.payout - once.amount
assert.equal((await c.rpc('wallet.balances')).usd, usd, 'retry charged once')
const tix = await c.rpc('dice.bet', { amount: 100, currency: 'tickets', target: 5000, over: true, idem_key: randomUUID() })
assert.equal(tix.balances.tickets, 1000 - 100 + tix.payout, 'tickets bet')

// Provably fair: rotate, then recompute the first bet's roll from the revealed seed.
const rev = await c.rpc('seed.rotate', { client_seed: 'smoke' })
const h = createHmac('sha256', rev.server_seed).update(`${rev.client_seed}:${bets[0].nonce}`).digest('hex')
const off = Number(BigInt('0x' + h.slice(0, 15)) % 47n)
assert.equal(Math.floor(Number(BigInt('0x' + h.slice(off, off + 14)) >> 3n) * 2 ** -53 * 10000), bets[0].roll, 'roll verifies')
assert.ok(c.pushes.some((p) => p.channel?.startsWith('user:') && p.pub?.data?.type === 'balances'), 'balance pushed to the user channel')

// Linked logins: a brand JWT attached to this account logs into it; nobody else can take it.
const sub = `smoke-${randomUUID()}`
assert.deepEqual(await c.rpc('auth.link', { provider: 'custom', credential: { token: jwt(sub) } }), {}, 'link')
const viaJwt = await loginWith('custom', { token: jwt(sub) })
assert.equal((await viaJwt.rpc('wallet.balances')).usd, usd, 'the linked login is the same account')
const stranger = await loginWith('custom', { token: jwt(`smoke-${randomUUID()}`) })
assert.equal((await stranger.rpc('auth.link', { provider: 'custom', credential: { token: jwt(sub) } })).error.code, 1011, 'identity taken')
assert.equal((await fetch(`${API}/auth/custom`, { method: 'POST', body: '{"token":"a.b.c"}' })).status, 401, 'bad jwt')
for (const conn of [c, viaJwt, stranger]) conn.close()
console.log(`smoke ok: user ${tg}, ${bets.length + 3} bets, usd ${usd}, roll verified`)
