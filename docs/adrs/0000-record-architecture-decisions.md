# ADR-0000: Record architecture decisions

**Status:** Accepted
**Date:** 2026-11-20
**Deciders:** the owner
**Evidence:** none needed

## Context
mydi's design was made while it was the MIDI part of a larger program, and measured there. Its decisions, and the evidence behind
them, should live with the library, numbered on their own, so the library can be read, used and changed without that program.

## Decision
- Every significant decision is an ADR in `docs/adrs/`, numbered `NNNN-short-title.md`, from `template.md`.
- An ADR may hold parts (A, B, C ...), each with its own status. Code cites a decision by its number and part (`ADR-0003 part D`).
- Status is `Proposed` until it is backed by code, a test or a measurement, then `Accepted`. A changed decision gets a new ADR that
  supersedes the old one; an accepted ADR is not rewritten.
- Each ADR names its **Evidence**: a test, a measurement in `docs/reports/`, or a cited source. Evidence is labelled: **[V-exp]**
  observed in an experiment (say where and on what), **[V-web]** read on a cited page, **[V-code]** read in a dependency's source,
  **[Inf]** inferred, not observed.
- The decisions mydi took over from the program it began in keep that program's part letters, so a part's letter means the same in
  the code, the ADR and the reports. Parts that were that program's alone are listed as such and decide nothing here.

## Options considered
| Option | For | Against |
|---|---|---|
| ADRs in the repository | Versioned with the code, reviewed with it | Discipline needed |
| Notes in the README | One file | Decisions and their evidence drift apart |

## Consequences
- Easier: knowing why the clock, the writers and the follower are built as they are, and what a change must measure.
- Harder: nothing material.
