---
title: "Callers, channels, and routing"
description: "Learn the entities and routing rules before configuring a pool of agents."
---

## Instance

An instance is one running Switchboard server backed by one SQLite database. It has a single operator identity, a dashboard password, and independent caller, agent, and administration credentials. Run **one server per database**. Management CLI operations can run alongside it.

## Caller and API key

A caller is an application submitting chat requests. Give each application a named API key so its requests and reconstructed conversations are distinguishable. A key can restrict allowed model names, set a requests-per-minute limit, override timeout, set priority, or pin requests to one channel.

API key names are display labels. Possession of the full key authorizes calls; a name or key prefix does not. See [security](../security/).

## Agent and channel

An agent is a system capable of reading a request and producing an assistant answer. A channel is that agent's serving instance, registered with `open_channel`. Channels declare model patterns and concurrency.

Use a descriptive channel name per agent instance, such as `claude-laptop` or `worker-2`. Reopening a name with its owning token reconnects that channel. Another token receives a distinct name and channel rather than taking it over, including when the original channel is offline.

## Model name

A model name is a routing label, not a guarantee that a particular upstream model performed inference. `switchboard/auto` works with a wildcard channel. A request labeled `gpt-4o` can be answered by any channel declaring `gpt-4o`, a matching pattern such as `gpt-4*`, or `*`.

By default arbitrary model names can queue. Turning off `accept_any_model` makes unknown model requests fail with `404`; see [settings](../settings/).

## Pull-based load balancing

Agents call `claim_request` to pull work. Switchboard atomically gives the next eligible request to the next available claimant. There is no push scheduler or fixed round-robin promise. A faster agent naturally claims more work.

Eligibility requires:

1. The channel's model pattern matches the request.
2. The channel is not draining and has fewer in-flight requests than its concurrency limit.
3. The key is not pinned to a different channel.
4. The request has not reached its deadline.

Higher key priority sorts first. Within a priority, oldest requests sort first. Pinning can intentionally leave work waiting even when other channels are idle.

## Channel status

- **Online:** activity is recent; the channel can claim work.
- **Draining:** no new claims; already held requests can finish.
- **Offline:** no activity within the stale interval. Held work is released or failed according to retry rules.
- **Closed:** intentionally removed from service; open a channel again to serve.

Channel activity and a request lease are separate clocks. A heartbeat keeps the channel visible, but `extend_lease` or streaming renews a particular request. Read the [request lifecycle](../lifecycle/) before tuning these intervals.
