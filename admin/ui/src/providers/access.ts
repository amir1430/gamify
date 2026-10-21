import type { AccessControlProvider } from '@refinedev/core'
import { loadMe } from './auth'

// can({action}): action is a permission (view, identity, limits, adjust, approve, admins, or a
// brand's own) checked against /api/me's permissions. Refine's own CRUD actions on a resource
// check its meta.perm, if any. The API enforces all of it again; this only hides what would fail.
const CRUD = new Set(['list', 'show', 'create', 'edit', 'delete', 'clone', 'field'])

export const accessControlProvider: AccessControlProvider = {
  async can({ action, params }) {
    const need: string | undefined = CRUD.has(action) ? params?.resource?.meta?.perm : action
    if (!need) return { can: true }
    const perms = await loadMe().then((m) => m.permissions, () => [] as string[])
    return perms.includes(need) ? { can: true } : { can: false, reason: "Your role can't do this" }
  },
  options: { buttons: { enableAccessControl: true, hideIfUnauthorized: true } },
}
