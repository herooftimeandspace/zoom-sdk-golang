# Security Policy

## Supported use

`zoom-sdk-golang` is intended to validate Zoom API responses and webhook payload
shapes while handling OAuth, retries, and structured logging safely.
It is not a complete security framework for Zoom integrations.

In particular:

- it does not currently verify webhook signatures for you
- it does not manage secret rotation
- it only redacts secrets from the structured fields it controls directly

## Reporting a vulnerability

If you discover a security issue, please report it privately to the maintainer
before opening a public issue.
