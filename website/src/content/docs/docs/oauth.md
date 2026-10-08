---
title: "MCP OAuth authorization"
description: "Explain discovery, client registration, user consent, PKCE, and token rotation."
---

OAuth allows an MCP client to obtain an agent credential without manually pasting a static token. Switchboard acts as both the protected MCP resource and its authorization server. It is not a sign-in-with-Google service, and it does not create additional operator accounts.

## Connection sequence

1. The client contacts `/mcp` without a valid token and receives `401` with a `WWW-Authenticate` header.
2. The header points to `/.well-known/oauth-protected-resource`. Its metadata identifies the authorization server.
3. The client reads `/.well-known/oauth-authorization-server` and registers through `/oauth/register` if necessary.
4. It opens `/oauth/authorize` with its client ID, registered redirect URI, state, and an S256 PKCE challenge.
5. The operator signs in to the dashboard and explicitly approves the client.
6. The client exchanges the short-lived, single-use code at `/oauth/token`, using the original verifier.
7. It sends the issued bearer token to `/mcp`.

## Consent and scope

The supported scope is `mcp`. Approval lets the client open channels, claim queued requests, and answer them. Inspect the name and redirect URI before approving. Authorizing an agent is a decision to let it read caller conversation contents; see [security](../security/).

## Tokens

Access tokens expire according to `oauth_access_ttl_seconds` (default 24 hours). Refresh tokens are valid within the configured issuance window (default 30 days). Refresh requires the client ID; confidential clients must authenticate. Rotation invalidates the old access and refresh values. Concurrent refresh attempts cannot both successfully reuse the same refresh token.

Revoke credentials in the dashboard's **Agents** page. `/oauth/revoke` accepts access-token revocation. It does not implement a general refresh-token revocation endpoint; revoke the associated token record through the operator UI or CLI.

## Public deployments

Set `public_url` to your external HTTPS base URL before connecting hosted clients. Discovery must advertise the address the client can reach, not the localhost socket behind a tunnel. Registered redirect URIs must exactly match authorization requests and cannot contain fragments.

If approval fails, check the callback URI, PKCE S256, client identity, public URL, and the browser's same-origin request. See [deployment](../deployment/) and [troubleshooting](../troubleshooting/).
