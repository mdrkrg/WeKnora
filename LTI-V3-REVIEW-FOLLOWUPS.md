# LTI v3 review follow-ups (deferred)

> Scope: findings from the `feat/lti-v3` code review that were **not** fixed in this
> session. The non-compiling-history finding was fixed separately.
> Branches: `feat/lti-v3` (main-based) and `feat/lti-v3-v0.7.2` (v0.7.2 backport).
> Line references are for `feat/lti-v3` unless stated; the two branches carry the
> same `internal/lti` tree, so fixes apply to both (cherry-pick after applying on v3).
> Fixes were intentionally left to a follow-up session; each item includes a
> description, the realistic trigger, and a suggested fix.

---

## 1. `Redeem` response double-encodes `roles` — Medium/Low

**File:** `internal/lti/handlers.go:387` (`Redeem`), `internal/lti/types.go` (`Ticket.Roles`),
`internal/lti/ticket.go:33-45`.

**Description:** `Ticket.Roles` is already a JSON-encoded **string** (`["role-a"]`, or `""`
when empty). The handler returns it verbatim as `"roles": ticket.Roles`, so the S2S wire
shape is:

```json
{ "roles": "[\"role-a\"]", ... }
```

and `"roles": ""` for a ticket with no roles — never a JSON array.

**Trigger:** Any S2S consumer that unmarshals `roles` as `[]string` breaks on non-empty
roles and sees a string for empty. In practice the shape is then double-decoded by the
consumer.

**Decision from this session:** keep the existing JSON-encoded string to preserve the
fork contract that the external web app already consumes. Only change this if the web
side is confirmed to expect an array.

**Suggested fix (only if the contract flips to an array):**

```go
var roles []string
if ticket.Roles != "" {
    _ = json.Unmarshal([]byte(ticket.Roles), &roles) // malformed -> empty, never 500
}
c.JSON(http.StatusOK, gin.H{
    "user_id":       ticket.UserID,
    "context_id":    ticket.ContextID,
    "roles":         roles,
    "access_token":  tokens.AccessToken,
    "refresh_token": tokens.RefreshToken,
})
```

**Note:** the same double-encoding exists in the legacy `feat/lti-v2` fork, so a change
must be coordinated with the web consumer rather than treated as a pure backport.

---

## 2. `Redeem` is missing the `enabled()` gate — Low

**File:** `internal/lti/handlers.go:304` (`Redeem`); compare `Launch` (`:170`),
`LoginInitiation` (`:95`), `JWKS` (`:285`), `Handoff` (`:411`).

**Description:** Every other LTI endpoint short-circuits when LTI is disabled, but `Redeem`
does not, and the route is mounted whenever the handler exists (always, via the container).

**Trigger:** An operator sets `LTI_ENABLE=false` (or a rollback/restart toggles it) but
`LTI_HANDOFF_SHARED_SECRET` remains set. A ticket issued just before disabling can still be
exchanged for a session during its `LTI_TICKET_TTL` window (default 120s).

**Suggested fix (at the top of `Redeem`, before the secret check):**

```go
if !h.enabled() {
    c.JSON(http.StatusNotFound, gin.H{"error": "lti disabled"})
    return
}
```

`Handoff` returns a bare 404; since `Redeem` is a JSON API, returning a JSON body is fine,
but a bare `c.Status(http.StatusNotFound)` is equally acceptable for consistency.

**Test to add:** `TestRedeemInertWhenDisabled` asserting 404 with `Enable: false` even with
a valid secret + ticket.

---

## 3. `not_a_member` is unreachable on the browser handoff path — Low (defensive only)

**File:** `internal/lti/handlers.go:453` (`Handoff`), `internal/lti/minter.go:56`
(`IssueDefault`), `frontend/src/App.vue` (`not_a_member` case restored by `c4b3854ce`).

**Description:** `Handoff` maps `ErrNotTenantMember -> not_a_member`, and the SPA now
renders a message for it, but `Handoff` only calls `IssueDefault`. `IssueDefault` maps only
`service.ErrNoDefaultWorkspace` and calls `IssueLTITokens(userID, 0, false)`, whose zero-tenant
branch never consults membership — so `ErrNotTenantMember` cannot reach the browser channel.

**Trigger:** None today; the mapping/branch are dead code. It becomes live only if a future
handoff path targets a tenant.

**Options:**
- Leave as defensive (current state) — harmless, and keeps parity with the legacy fork.
- Remove the `Handoff` mapping and the SPA string to drop dead code.
- Make it real by having `IssueDefault` perform the same `ErrMembershipNotFound ->
  ErrNotTenantMember` mapping when the resolved home tenant membership is not active.

Pick one deliberately; the current commit message ("the self-handoff contract still surfaces
not_a_member") overstates reality.

---

## 4. `Redeem` mint-failure denials are not audited — Low

**File:** `internal/lti/handlers.go:344-368` (`Redeem` mint-failure branches); compare
`Handoff` at `:443-470`.

**Description:** On `ErrNotTenantMember`, `ErrNoWorkspace`, or a generic mint error, `Redeem`
restores the ticket and returns 403/500 without emitting an audit row. `Handoff` emits
`lti.ticket_redeem_denied` for the equivalent failures.

**Trigger:** The `Redeem` caller holds the shared secret and can submit arbitrary
`tenant_id` values. Denied tenant targeting (a potentially interesting signal) produces no
audit trail, while the same class of event on the browser path does.

**Suggested fix:** before each failure `return`, emit:

```go
h.emitAudit(c.Request.Context(), &types.AuditLog{
    Action:        AuditActionLTITicketRedeemDenied,
    ActorUserID:   ticket.UserID,
    TargetType:    "lti_ticket",
    RequestPath:   c.Request.URL.Path,
    RequestMethod: c.Request.Method,
    Outcome:       types.AuditOutcomeDenied,
    Details: auditDetailsJSON(map[string]any{
        "reason":     reason,          // "not_a_member" | "no_workspace" | "server_error"
        "user_id":    ticket.UserID,
        "context_id": ticket.ContextID,
        "tenant_id":  requestedTenant, // when present
    }),
})
```

Mirroring `Handoff`'s reason strings keeps the two channels queryable the same way.

**Test to add:** assert one denied audit entry (with `tenant_id`) for the non-member 403.

---

## 5. `validSharedSecret` length pre-check leaks secret length — Nit

**File:** `internal/lti/handlers.go:551-566`.

**Description:** `if cfg == "" || token == "" || len(cfg) != len(token) { return false }`
returns before `subtle.ConstantTimeCompare`, so a timing observer can distinguish a
wrong-length token from a same-length-but-wrong token. Impact is low for a fixed shared
secret, and `subtle.ConstantTimeCompare` itself returns early on unequal lengths anyway.

**Suggested fix (only if hardening is desired):** compare fixed-width digests instead:

```go
sumCfg := sha256.Sum256([]byte(cfg))
sumTok := sha256.Sum256([]byte(token))
return subtle.ConstantTimeCompare(sumCfg[:], sumTok[:]) == 1
```

Drop the `len(cfg) != len(token)` short-circuit in that case. Keeping the current behavior
is acceptable.

---

## Reference: what was verified for the fixed item

The only change made this session was to the commit history (not to any tip tree): the
`ErrNotTenantMember` definition was moved into the earliest commit that references it
(`feat/lti-embed-self`), removed again by the scoping commit, and re-added by the redeem
commit. Every commit that contains `internal/lti` now builds on both branches; the tree at
each branch tip is byte-identical to before the rewrite.
