---
title: "Choose a low-cost host"
description: "Match a persistent agent gateway to existing hardware, free-tier VMs, or a small paid VPS."
---

Switchboard needs one persistent server process, durable database storage, and connections that can remain open while an agent thinks. A small VM or existing machine is a better match than a static host or an unchanged serverless-function deployment.

## Lowest cost: hardware you already have

An existing desktop, mini-PC, home server or VPS can run Compose with no additional hosting subscription. Electricity, internet connectivity and backups still have costs. Keep it private through SSH, a VPN, or an appropriate tunnel. The [Keel SSH deployment](../keel/) works with such a host.

## Zero-cost cloud candidate: Oracle Always Free

As checked on 8 October 2026, [Oracle's official Always Free documentation](https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm) lists an Ampere A1 allocation equivalent to **2 OCPUs and 12 GB RAM** for Always Free tenancies, plus a shared free block-storage allowance. That is enough for a modest Switchboard service and PostgreSQL container. Older references to 4 OCPUs/24 GB should not be assumed to apply to a current free tenancy.

Use an Always Free-eligible image and shape in the home region, stay within the account's displayed quotas, and check billing before provisioning. Shape availability can be limited. Oracle may reclaim idle instances; free compute is not a reliability guarantee. Do not generate artificial load to evade policy. Preserve backups outside the VM and keep a reproducible deployment.

The deployment recipe uses Ubuntu ARM64, Docker Compose, and optional Caddy. It does not depend on Oracle-specific APIs and also works on another Linux host if capacity is unavailable. A custom domain may cost money even when compute is free.

## Cheapest reliable option

A small paid VPS avoids relying on free capacity and idle-instance policies. Compare the current total price including RAM, IPv4, storage, backups, tax and transfer—not just the promotional base rate. The same Compose/SSH target lets you move providers without changing the application.

## Why not Render Free for persistent operation?

[Render's free-service limits](https://render.com/docs/free) include idle spin-down and an ephemeral filesystem; its free PostgreSQL databases expire after 30 days. Those limits make it a poor long-term home for an agent gateway. No payment does not automatically mean reliable persistent hosting.

## Keep the website separate

GitHub Pages hosts the landing page and documentation for free. It cannot run Switchboard's live API, MCP transport or PostgreSQL. [Configure Pages](../github-pages/) independently from the chosen runtime host.
