# JSON service

Makes any service that answers a GET with JSON readable as `home.plugin("http-json").data()`, refreshed every 5 minutes.
A failure keeps the last good data, and `ageSeconds()` shows how old it is. For a second service, copy the folder and change its `id`.
For devices from your own service, use [service-bridge](../service-bridge/README.md) instead.

Tested against a fake HTTP server through the real extension host. MIT, like the rest of the repository.
