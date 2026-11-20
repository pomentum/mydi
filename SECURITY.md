# Security

## Supported versions

mydi is at `v0`. Fixes go into the newest release only; update to it to receive them.

| Version | Supported |
|---|---|
| The newest `v0.x.y` | Yes |
| Any older one | No |

## Reporting a vulnerability

Report it privately through GitHub: on this repository, open **Security**, then **Report a vulnerability**
([direct link](https://github.com/pomentum/mydi/security/advisories/new)). Please do not open a public issue for it.

Say what is affected (the version, the system: Linux or Windows), how to reproduce it, and what it lets someone do. A report is
acknowledged within a week. A confirmed vulnerability is fixed in a new release, published with a GitHub security advisory that credits
the reporter unless they ask otherwise.

## Scope

mydi opens the machine's MIDI ports and plays what a program sends it. It opens no network connection, and reads no files but the
system's MIDI devices (on Linux `/dev/snd` and the names in `/proc/asound`) and the folder of virtual ports a program names. A program that passes untrusted MIDI or JSON from elsewhere to mydi is in scope: a message that crashes the
hub, holds a port or a thread, or reaches past the folder of a virtual port is a vulnerability. What a program does with the ports it
allows, or with what it hears, is that program's.
