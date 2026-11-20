# The hub's MIDI clock spike on Windows

How close can a Go program with no CGo put a 24 ppqn MIDI clock to its grid on Windows, and which way of waiting gets it there
(ADR-0002 part B, ADR-0156 part H)? `main.go` runs the clock at 120 BPM (a pulse every 20.833 ms) with one strategy after another, times
each pulse with `QueryPerformanceCounter`, and prints a table. It has its own `go.mod` (only `golang.org/x/sys`) and is not part of the
hub's module. The findings and the design they lead to are in
[the report](../midi-clock-on-windows.md).

It changes nothing on the machine. Every setting it makes is its own thread's (priority, MMCSS) or its own process's (power throttling,
`timeBeginPeriod`), and each is undone after the strategy that uses it; a thread whose priority was raised ends with its goroutine. No
power plan, registry value, service or privilege is touched.

## The strategies

| Key | How it waits for the next pulse |
|---|---|
| a | `time.Sleep` until the pulse, on an ordinary goroutine (what Go gives you) |
| b | `time.Ticker` (the pulse's slot is the nearest to when it woke; dropped ticks are counted as missed) |
| s | a, waking 1 ms early and spinning on `QueryPerformanceCounter` to the pulse |
| c | its own high-resolution waitable timer (`CreateWaitableTimerExW` with `CREATE_WAITABLE_TIMER_HIGH_RESOLUTION`) on a locked OS thread |
| p | c with `THREAD_PRIORITY_TIME_CRITICAL` |
| q | p, waking 1 ms early and spinning to the pulse |
| m | c with MMCSS "Pro Audio" (`AvSetMmThreadCharacteristicsW`, `AvSetMmThreadPriority` critical) |
| d | c with MMCSS and `THREAD_PRIORITY_TIME_CRITICAL` |
| r | d, its timer aimed early by the median lateness of its last 32 pulses (at most 2 ms), never spinning |
| e | d, waking 1 ms early and spinning to the pulse |
| f | e with the process opted out of power throttling (`SetProcessInformation`: EcoQoS and ignored timer resolution both off) |
| g | a with `timeBeginPeriod(1)` for the process |
| h | f with `timeBeginPeriod(1)` |

Each strategy runs in several rounds (3 of 15 s by default), every strategy once a round, so a change in the machine's load falls on
all of them. Columns: lateness against the ideal grid (median, 95th and 99th percentile, maximum); the longest gap between two pulses
(20.833 ms is perfect); the RMS of each interval's error, which is what a receiver reading tempo sees; pulses more than 1 ms and 5 ms
late; the time spent spinning a pulse; the process's CPU cycles a second; and how busy the whole machine was. `-midi "GS Wavetable"`
sends each pulse (`F8`) to a MIDI output through `midiOutShortMsg` and times the call, then sends All Notes Off, Stop and a reset;
`-gcload` keeps the garbage collector busy in the same process, as fast as it can or at `-gcrate` MB a second; `-burn 16 -for 10m` is a CPU load to run beside it, as a second process.

## Run it

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD:/src" -w /src -e CGO_ENABLED=0 -e GOOS=windows golang:1.27.1-bookworm go build -trimpath -o spike.exe .
./spike.exe                                   # every strategy, about 9 minutes
./spike.exe -burn 16 -for 10m & ./spike.exe   # the same beside a CPU load on 16 threads
./spike.exe -gcload                           # with the collector busy (-gcrate 20 for a moderate rate)
./spike.exe -midi "GS Wavetable" -strategies a,d,r,e,h
rm spike.exe                                  # never committed
```

## What it found (2026-11-13)

[V-exp] One laptop: an 8-core, 16-thread mobile processor, Windows 11, the Balanced plan, on mains;
`QueryPerformanceCounter` at 10 MHz; the system timer at 1.000 ms, raised by some other program (default 15.625 ms, finest 0.5 ms);
Windows MIDI Services' `midisrv` running. A busy developer machine: a container engine's VM with many containers, endpoint
protection, chat and browser processes and other builds and tests. **No idle state was available**: the machine read 80
to 100% busy before the spike started. Go 1.27.1, `GOMAXPROCS` 16.

| Run | Load | Command |
|---|---|---|
| Pass 1 | As found, its heaviest moment (an earlier build: 3 rounds of 20 s, no s, q, m or r) | `./spike.exe -seg 20s` |
| A | As found, 80 to 88% busy | `./spike.exe` |
| B | As found plus 16 spinning threads of its own: 100% | `./spike.exe -burn 16 -for 10m & ./spike.exe` |
| C | As B, Go's asynchronous preemption off | `GODEBUG=asyncpreemptoff=1 ./spike.exe -strategies p,q,d,e,f,h` beside `-burn 16` |
| D | As B, preemption on, straight after C | `./spike.exe -strategies p,q,d,e,f,h` beside `-burn 16` |
| E | As found, an allocation loop in the same process | `./spike.exe -gcload -strategies a,c,p,d,r,e` |
| F | As found, each pulse sent to the Microsoft GS Wavetable Synth | `./spike.exe -midi "GS Wavetable" -strategies a,d,r,e,h` |
| G | As found, every strategy again, at the end | `./spike.exe` |
| E2 | As found, the collector at a moderate rate | `./spike.exe -gcload -gcrate 20 -strategies a,d,r`, then `-gcrate 100` |

In short (the report reads them): Go's sleep and ticker are fine with headroom and not under load, and the ticker drops pulses; a
thread with its own high-resolution timer needs priority to be any better; MMCSS "Pro Audio" with `THREAD_PRIORITY_TIME_CRITICAL` (d)
held the 99th percentile under 1 ms in eight runs (not pass 1, the machine's worst moment, nor E, the allocation loop), at no extra
CPU, and aimed early by its recent wake-up delay (r) put the median on the grid at some cost in its tail; spinning makes the median
perfect and, under full load, stalls the thread for seconds; an allocation loop in the same process stalls every strategy, while a
moderate collector (E2) only lengthens d's worst pulse; `midiOutShortMsg` costs about 0.2 ms a call here and must be made from the
prioritised thread.

### Every run

Lateness is against the ideal grid; the longest gap is between two pulses (20.833 ms is perfect); the interval jitter is the RMS of each
interval's error; CPU is the spike's own process; machine busy is the whole machine over the strategy's time.

#### Pass 1 (an earlier build: 3 rounds of 20 s)

- time.Now monotonic step: median 0.571 ms, max 153.215 ms (70 steps in 300 ms)
- time.Sleep(1ms) takes: median 1.589 ms, p99 11.766 ms, max 12.361 ms

| | Strategy | Pulses | Median late | p95 | p99 | Max late | Longest gap | Interval jitter (RMS) | > 1 ms | > 5 ms | Missed | Drift | Spin a pulse | CPU |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| a | time.Sleep to the next pulse | 2877 | 0.544 ms | 6.793 ms | 32.422 ms | 208.00 ms | 208.71 ms | 5.923 ms | 686 | 194 | 0 | 1.227 ms | 0 µs | 20 Mcycles/s |
| b | time.Ticker | 2747 | 0.455 ms | 5.788 ms | 9.415 ms | 10.40 ms | 88.11 ms | 7.750 ms | 748 | 183 | 237 | 0.338 ms | 0 µs | 19 Mcycles/s |
| c | own high-res waitable timer, locked thread | 2877 | 0.459 ms | 2.681 ms | 6.763 ms | 37.02 ms | 57.28 ms | 1.996 ms | 386 | 52 | 0 | 0.166 ms | 0 µs | 21 Mcycles/s |
| p | c + TIME_CRITICAL | 2877 | 0.389 ms | 0.721 ms | 18.138 ms | 95.98 ms | 116.46 ms | 4.438 ms | 109 | 66 | 0 | 0.323 ms | 0 µs | 29 Mcycles/s |
| d | c + MMCSS Pro Audio + TIME_CRITICAL | 2877 | 0.388 ms | 0.639 ms | 5.569 ms | 85.44 ms | 105.71 ms | 3.006 ms | 63 | 29 | 0 | 3.571 ms | 0 µs | 23 Mcycles/s |
| e | d + spin to the pulse | 2877 | 0.001 ms | 2668.295 ms | 5063.948 ms | 5647.25 ms | 5668.08 ms | 105.879 ms | 636 | 484 | 0 | 6.398 ms | 3724 µs | 104 Mcycles/s |
| f | e + power throttling off | 2877 | 0.001 ms | 11.012 ms | 53.014 ms | 519.27 ms | 539.43 ms | 11.901 ms | 405 | 212 | 0 | 0.821 ms | 1839 µs | 98 Mcycles/s |
| g | a + timeBeginPeriod(1) | 2877 | 0.483 ms | 45.972 ms | 144.279 ms | 303.43 ms | 320.42 ms | 15.695 ms | 875 | 476 | 0 | 3.914 ms | 0 µs | 17 Mcycles/s |
| h | f + timeBeginPeriod(1) | 2877 | 0.001 ms | 2.348 ms | 49.032 ms | 241.13 ms | 261.96 ms | 6.965 ms | 209 | 104 | 0 | 27.786 ms | 1165 µs | 95 Mcycles/s |

#### Run A

- time.Now monotonic step: median 0.578 ms, max 9.531 ms (374 steps in 300 ms)
- time.Sleep(1ms) takes: median 1.570 ms, p99 4.294 ms, max 9.468 ms

| | Strategy | Pulses | Median late | p95 | p99 | Max late | Longest gap | Interval jitter (RMS) | > 1 ms | > 5 ms | Missed | Drift | Spin a pulse | CPU | Machine busy |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| a | time.Sleep to the next pulse | 2157 | 0.455 ms | 2.278 ms | 6.275 ms | 19.22 ms | 39.92 ms | 1.511 ms | 260 | 31 | 0 | 0.191 ms | 0 µs | 15 Mcycles/s | 87% |
| b | time.Ticker | 2084 | 0.204 ms | 3.999 ms | 8.083 ms | 10.40 ms | 510.53 ms | 12.146 ms | 306 | 79 | 127 | 0.504 ms | 0 µs | 16 Mcycles/s | 84% |
| s | a + spin to the pulse | 2157 | 0.001 ms | 3.652 ms | 11.190 ms | 33.92 ms | 51.38 ms | 2.669 ms | 251 | 79 | 0 | 2.616 ms | 551 µs | 62 Mcycles/s | 86% |
| c | own high-res waitable timer, locked thread | 2157 | 0.512 ms | 11.902 ms | 93.020 ms | 451.29 ms | 417.76 ms | 10.432 ms | 642 | 269 | 0 | 2.166 ms | 0 µs | 14 Mcycles/s | 84% |
| p | c + TIME_CRITICAL | 2157 | 0.372 ms | 0.604 ms | 0.816 ms | 7.86 ms | 28.20 ms | 0.314 ms | 7 | 1 | 0 | 0.065 ms | 0 µs | 12 Mcycles/s | 88% |
| q | p + spin to the pulse | 2157 | 0.001 ms | 0.336 ms | 1.749 ms | 21.33 ms | 42.16 ms | 0.848 ms | 41 | 5 | 0 | 0.464 ms | 702 µs | 99 Mcycles/s | 85% |
| m | c + MMCSS Pro Audio | 2157 | 0.360 ms | 0.601 ms | 0.754 ms | 1.21 ms | 21.87 ms | 0.235 ms | 5 | 0 | 0 | 0.047 ms | 0 µs | 14 Mcycles/s | 84% |
| d | c + MMCSS Pro Audio + TIME_CRITICAL | 2157 | 0.378 ms | 0.620 ms | 0.973 ms | 3.50 ms | 23.87 ms | 0.276 ms | 20 | 0 | 0 | 0.043 ms | 0 µs | 13 Mcycles/s | 84% |
| e | d + spin to the pulse | 2157 | 0.001 ms | 0.141 ms | 0.861 ms | 14.13 ms | 34.97 ms | 0.676 ms | 20 | 2 | 0 | 0.303 ms | 673 µs | 101 Mcycles/s | 80% |
| f | e + power throttling off | 2157 | 0.001 ms | 0.080 ms | 1.083 ms | 14.97 ms | 35.81 ms | 0.803 ms | 22 | 6 | 0 | 0.722 ms | 688 µs | 101 Mcycles/s | 81% |
| g | a + timeBeginPeriod(1) | 2157 | 0.373 ms | 1.602 ms | 6.005 ms | 38.33 ms | 56.82 ms | 1.527 ms | 171 | 24 | 0 | 1.721 ms | 0 µs | 14 Mcycles/s | 81% |
| h | f + timeBeginPeriod(1) | 2157 | 0.001 ms | 0.107 ms | 0.507 ms | 2.71 ms | 23.54 ms | 0.158 ms | 6 | 0 | 0 | 0.020 ms | 646 µs | 100 Mcycles/s | 84% |

#### Run B

- time.Now monotonic step: median 0.999 ms, max 154.285 ms (43 steps in 300 ms)
- time.Sleep(1ms) takes: median 5.848 ms, p99 24.901 ms, max 29.969 ms

| | Strategy | Pulses | Median late | p95 | p99 | Max late | Longest gap | Interval jitter (RMS) | > 1 ms | > 5 ms | Missed | Drift | Spin a pulse | CPU | Machine busy |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| a | time.Sleep to the next pulse | 2157 | 5.363 ms | 18.847 ms | 29.687 ms | 71.14 ms | 82.47 ms | 8.535 ms | 1864 | 1149 | 0 | 0.981 ms | 0 µs | 11 Mcycles/s | 100% |
| b | time.Ticker | 1844 | 2.453 ms | 9.396 ms | 10.143 ms | 10.41 ms | 167.82 ms | 13.942 ms | 1169 | 574 | 597 | 1.539 ms | 0 µs | 10 Mcycles/s | 100% |
| s | a + spin to the pulse | 2157 | 7.716 ms | 52.408 ms | 436.425 ms | 873.74 ms | 885.29 ms | 26.387 ms | 1818 | 1331 | 0 | 2.054 ms | 574 µs | 14 Mcycles/s | 100% |
| c | own high-res waitable timer, locked thread | 2157 | 11.511 ms | 45.422 ms | 69.527 ms | 121.02 ms | 133.45 ms | 18.424 ms | 1985 | 1549 | 0 | 13.665 ms | 0 µs | 7 Mcycles/s | 100% |
| p | c + TIME_CRITICAL | 2157 | 0.335 ms | 0.561 ms | 0.650 ms | 18.87 ms | 39.41 ms | 0.676 ms | 7 | 2 | 0 | 0.020 ms | 0 µs | 9 Mcycles/s | 100% |
| q | p + spin to the pulse | 2157 | 0.001 ms | 1841.044 ms | 2867.654 ms | 3305.13 ms | 3325.97 ms | 95.942 ms | 469 | 459 | 0 | 65.080 ms | 4872 µs | 163 Mcycles/s | 100% |
| m | c + MMCSS Pro Audio | 2157 | 0.328 ms | 0.563 ms | 0.723 ms | 27.94 ms | 48.21 ms | 0.963 ms | 12 | 5 | 0 | 0.044 ms | 0 µs | 10 Mcycles/s | 100% |
| d | c + MMCSS Pro Audio + TIME_CRITICAL | 2157 | 0.335 ms | 0.566 ms | 0.705 ms | 1.96 ms | 22.48 ms | 0.236 ms | 7 | 0 | 0 | 0.056 ms | 0 µs | 9 Mcycles/s | 100% |
| e | d + spin to the pulse | 2157 | 0.001 ms | 11025.778 ms | 12817.317 ms | 13254.79 ms | 13275.62 ms | 287.060 ms | 679 | 642 | 0 | 5303.376 ms | 8581 µs | 63 Mcycles/s | 100% |
| f | e + power throttling off | 2157 | 0.001 ms | 1114.322 ms | 2280.506 ms | 2717.98 ms | 2738.82 ms | 73.866 ms | 363 | 339 | 0 | 1136.977 ms | 4153 µs | 69 Mcycles/s | 100% |
| g | a + timeBeginPeriod(1) | 2157 | 4.030 ms | 89.832 ms | 165.547 ms | 383.58 ms | 403.94 ms | 23.568 ms | 1406 | 1000 | 0 | 49.867 ms | 0 µs | 9 Mcycles/s | 100% |
| h | f + timeBeginPeriod(1) | 2157 | 0.001 ms | 4.348 ms | 30.132 ms | 132.48 ms | 153.32 ms | 6.320 ms | 138 | 104 | 0 | 2.299 ms | 1379 µs | 82 Mcycles/s | 100% |

#### Run C (asynchronous preemption off)

- time.Now monotonic step: median 1.000 ms, max 57.352 ms (150 steps in 300 ms)
- time.Sleep(1ms) takes: median 8.548 ms, p99 36.329 ms, max 50.009 ms

| | Strategy | Pulses | Median late | p95 | p99 | Max late | Longest gap | Interval jitter (RMS) | > 1 ms | > 5 ms | Missed | Drift | Spin a pulse | CPU | Machine busy |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| p | c + TIME_CRITICAL | 2157 | 0.332 ms | 0.570 ms | 0.709 ms | 58.47 ms | 78.73 ms | 1.531 ms | 13 | 7 | 0 | 0.191 ms | 0 µs | 10 Mcycles/s | 100% |
| q | p + spin to the pulse | 2157 | 0.001 ms | 19.331 ms | 144.114 ms | 335.11 ms | 355.94 ms | 13.889 ms | 212 | 164 | 0 | 60.482 ms | 1986 µs | 64 Mcycles/s | 100% |
| d | c + MMCSS Pro Audio + TIME_CRITICAL | 2157 | 0.324 ms | 0.572 ms | 0.777 ms | 3.99 ms | 24.65 ms | 0.282 ms | 15 | 0 | 0 | 0.074 ms | 0 µs | 8 Mcycles/s | 100% |
| e | d + spin to the pulse | 2157 | 0.001 ms | 0.069 ms | 42.406 ms | 141.78 ms | 162.61 ms | 6.625 ms | 90 | 76 | 0 | 9.274 ms | 1239 µs | 76 Mcycles/s | 100% |
| f | e + power throttling off | 2157 | 0.001 ms | 17.805 ms | 180.298 ms | 451.12 ms | 471.95 ms | 15.546 ms | 168 | 150 | 0 | 9.329 ms | 1875 µs | 66 Mcycles/s | 100% |
| h | f + timeBeginPeriod(1) | 2157 | 0.001 ms | 7.715 ms | 101.780 ms | 268.44 ms | 289.27 ms | 11.479 ms | 136 | 115 | 0 | 0.725 ms | 1522 µs | 68 Mcycles/s | 100% |

#### Run D

- time.Now monotonic step: median 0.728 ms, max 1.540 ms (41 steps in 300 ms)
- time.Sleep(1ms) takes: median 1.544 ms, p99 25.203 ms, max 41.313 ms

| | Strategy | Pulses | Median late | p95 | p99 | Max late | Longest gap | Interval jitter (RMS) | > 1 ms | > 5 ms | Missed | Drift | Spin a pulse | CPU | Machine busy |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| p | c + TIME_CRITICAL | 2157 | 0.335 ms | 0.574 ms | 1.043 ms | 22.89 ms | 43.19 ms | 1.473 ms | 25 | 10 | 0 | 0.038 ms | 0 µs | 12 Mcycles/s | 100% |
| q | p + spin to the pulse | 2157 | 0.001 ms | 178.540 ms | 1806.292 ms | 2243.54 ms | 2264.36 ms | 50.177 ms | 326 | 294 | 0 | 19.321 ms | 2900 µs | 73 Mcycles/s | 100% |
| d | c + MMCSS Pro Audio + TIME_CRITICAL | 2157 | 0.333 ms | 0.570 ms | 0.666 ms | 11.33 ms | 31.90 ms | 0.434 ms | 9 | 1 | 0 | 0.207 ms | 0 µs | 11 Mcycles/s | 100% |
| e | d + spin to the pulse | 2157 | 0.001 ms | 786.363 ms | 2577.722 ms | 3015.20 ms | 3036.03 ms | 65.949 ms | 369 | 330 | 0 | 1.279 ms | 3384 µs | 76 Mcycles/s | 100% |
| f | e + power throttling off | 2157 | 0.001 ms | 7.357 ms | 33.012 ms | 143.40 ms | 164.23 ms | 6.878 ms | 158 | 124 | 0 | 1.437 ms | 1502 µs | 79 Mcycles/s | 100% |
| h | f + timeBeginPeriod(1) | 2157 | 0.001 ms | 10.100 ms | 44.246 ms | 357.07 ms | 377.90 ms | 10.018 ms | 223 | 164 | 0 | 1.184 ms | 1735 µs | 83 Mcycles/s | 100% |

#### Run E (-gcload)

- time.Now monotonic step: median 0.567 ms, max 0.759 ms (15 steps in 300 ms)
- time.Sleep(1ms) takes: median 1.616 ms, p99 23.415 ms, max 26.058 ms

| | Strategy | Pulses | Median late | p95 | p99 | Max late | Longest gap | Interval jitter (RMS) | > 1 ms | > 5 ms | Missed | Drift | Spin a pulse | CPU | Machine busy |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| a | time.Sleep to the next pulse | 2157 | 0.700 ms | 48.600 ms | 158.904 ms | 350.42 ms | 371.06 ms | 17.356 ms | 914 | 497 | 0 | 15.886 ms | 0 µs | 1132 Mcycles/s | 100% |
| c | own high-res waitable timer, locked thread | 2157 | 1.134 ms | 33.439 ms | 141.390 ms | 346.61 ms | 349.68 ms | 15.076 ms | 1113 | 566 | 0 | 15.166 ms | 0 µs | 820 Mcycles/s | 100% |
| p | c + TIME_CRITICAL | 2157 | 0.391 ms | 115.549 ms | 1344.675 ms | 1782.14 ms | 1802.60 ms | 40.693 ms | 205 | 174 | 0 | 1222.619 ms | 0 µs | 836 Mcycles/s | 99% |
| d | c + MMCSS Pro Audio + TIME_CRITICAL | 2157 | 0.416 ms | 74.404 ms | 824.375 ms | 1136.87 ms | 1157.59 ms | 33.008 ms | 261 | 193 | 0 | 2.944 ms | 0 µs | 1331 Mcycles/s | 99% |
| r | d, aimed early by its recent median lateness | 2157 | 0.001 ms | 7.255 ms | 97.385 ms | 368.18 ms | 389.38 ms | 11.075 ms | 183 | 122 | 0 | 16.614 ms | 0 µs | 1383 Mcycles/s | 99% |
| e | d + spin to the pulse | 2157 | 0.001 ms | 5.110 ms | 83.928 ms | 382.95 ms | 403.78 ms | 11.021 ms | 186 | 109 | 0 | 7.331 ms | 794 µs | 1651 Mcycles/s | 98% |

- a: 243 collections, 3649.72 ms of stop-the-world pauses
- c: 242 collections, 2088.35 ms of stop-the-world pauses
- p: 241 collections, 2105.14 ms of stop-the-world pauses
- d: 476 collections, 2815.59 ms of stop-the-world pauses
- r: 484 collections, 2341.34 ms of stop-the-world pauses
- e: 586 collections, 2281.97 ms of stop-the-world pauses

#### Run F (-midi)

- time.Now monotonic step: median 0.590 ms, max 8.196 ms (361 steps in 300 ms)
- time.Sleep(1ms) takes: median 1.607 ms, p99 5.411 ms, max 10.225 ms
- MIDI out: "Microsoft GS Wavetable Synth" opened in 1.427s

| | Strategy | Pulses | Median late | p95 | p99 | Max late | Longest gap | Interval jitter (RMS) | > 1 ms | > 5 ms | Missed | Drift | Spin a pulse | CPU | Machine busy |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| a | time.Sleep to the next pulse | 2157 | 0.780 ms | 3759.093 ms | 4452.336 ms | 4533.28 ms | 3266.80 ms | 71.667 ms | 1001 | 677 | 0 | 1336.774 ms | 0 µs | 35 Mcycles/s | 98% |
| d | c + MMCSS Pro Audio + TIME_CRITICAL | 2157 | 0.377 ms | 0.617 ms | 0.977 ms | 3.88 ms | 24.29 ms | 0.306 ms | 19 | 0 | 0 | 0.064 ms | 0 µs | 37 Mcycles/s | 99% |
| r | d, aimed early by its recent median lateness | 2157 | 0.003 ms | 0.312 ms | 0.826 ms | 12.36 ms | 33.16 ms | 0.473 ms | 12 | 1 | 0 | 0.277 ms | 0 µs | 36 Mcycles/s | 97% |
| e | d + spin to the pulse | 2157 | 0.001 ms | 2.887 ms | 266.307 ms | 540.40 ms | 561.23 ms | 15.900 ms | 156 | 87 | 0 | 131.128 ms | 1437 µs | 107 Mcycles/s | 99% |
| h | f + timeBeginPeriod(1) | 2157 | 0.001 ms | 1045.713 ms | 2749.716 ms | 3184.24 ms | 3205.07 ms | 73.112 ms | 299 | 242 | 0 | 1.923 ms | 2834 µs | 152 Mcycles/s | 98% |

- a: midiOutShortMsg took median 266.8 µs, p99 71490.0 µs, max 266368.9 µs
- d: midiOutShortMsg took median 221.5 µs, p99 356.7 µs, max 6674.9 µs
- r: midiOutShortMsg took median 219.7 µs, p99 400.8 µs, max 33153.3 µs
- e: midiOutShortMsg took median 219.8 µs, p99 414.4 µs, max 1960.3 µs
- h: midiOutShortMsg took median 213.9 µs, p99 402.2 µs, max 79691.9 µs

#### Run G

- time.Now monotonic step: median 0.604 ms, max 54.764 ms (161 steps in 300 ms)
- time.Sleep(1ms) takes: median 1.551 ms, p99 11.388 ms, max 17.204 ms

| | Strategy | Pulses | Median late | p95 | p99 | Max late | Longest gap | Interval jitter (RMS) | > 1 ms | > 5 ms | Missed | Drift | Spin a pulse | CPU | Machine busy |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| a | time.Sleep to the next pulse | 2157 | 0.492 ms | 6.111 ms | 18.641 ms | 43.27 ms | 60.05 ms | 3.442 ms | 401 | 132 | 0 | 4.250 ms | 0 µs | 17 Mcycles/s | 93% |
| b | time.Ticker | 2131 | 0.439 ms | 5.115 ms | 8.559 ms | 10.37 ms | 85.92 ms | 4.489 ms | 447 | 109 | 76 | 1.272 ms | 0 µs | 15 Mcycles/s | 90% |
| s | a + spin to the pulse | 2157 | 0.036 ms | 16.997 ms | 45.118 ms | 204.45 ms | 215.23 ms | 8.375 ms | 562 | 292 | 0 | 8.347 ms | 532 µs | 53 Mcycles/s | 98% |
| c | own high-res waitable timer, locked thread | 2157 | 0.507 ms | 3.378 ms | 7.276 ms | 20.46 ms | 41.06 ms | 1.800 ms | 433 | 52 | 0 | 0.584 ms | 0 µs | 22 Mcycles/s | 96% |
| p | c + TIME_CRITICAL | 2157 | 0.388 ms | 0.614 ms | 1.055 ms | 7.29 ms | 27.91 ms | 0.444 ms | 24 | 2 | 0 | 0.084 ms | 0 µs | 18 Mcycles/s | 96% |
| q | p + spin to the pulse | 2157 | 0.001 ms | 7.999 ms | 125.477 ms | 551.25 ms | 572.08 ms | 13.475 ms | 251 | 138 | 0 | 4.024 ms | 1650 µs | 94 Mcycles/s | 98% |
| m | c + MMCSS Pro Audio | 2157 | 0.370 ms | 0.610 ms | 0.806 ms | 9.50 ms | 29.84 ms | 0.403 ms | 14 | 2 | 0 | 0.164 ms | 0 µs | 24 Mcycles/s | 90% |
| d | c + MMCSS Pro Audio + TIME_CRITICAL | 2157 | 0.378 ms | 0.612 ms | 0.720 ms | 1.90 ms | 22.24 ms | 0.234 ms | 5 | 0 | 0 | 0.037 ms | 0 µs | 16 Mcycles/s | 93% |
| r | d, aimed early by its recent median lateness | 2157 | 0.002 ms | 0.331 ms | 0.594 ms | 3.91 ms | 24.55 ms | 0.294 ms | 3 | 0 | 0 | 0.295 ms | 0 µs | 20 Mcycles/s | 91% |
| e | d + spin to the pulse | 2157 | 0.001 ms | 2.783 ms | 20.159 ms | 320.14 ms | 335.88 ms | 7.697 ms | 154 | 83 | 0 | 6.119 ms | 1200 µs | 97 Mcycles/s | 92% |
| f | e + power throttling off | 2157 | 0.001 ms | 0.285 ms | 2.684 ms | 17.02 ms | 37.85 ms | 1.009 ms | 43 | 12 | 0 | 0.956 ms | 723 µs | 98 Mcycles/s | 91% |
| g | a + timeBeginPeriod(1) | 2157 | 0.418 ms | 2.855 ms | 9.340 ms | 48.29 ms | 54.19 ms | 2.289 ms | 289 | 55 | 0 | 0.921 ms | 0 µs | 18 Mcycles/s | 91% |
| h | f + timeBeginPeriod(1) | 2157 | 0.001 ms | 0.431 ms | 5.663 ms | 35.32 ms | 56.15 ms | 1.980 ms | 62 | 24 | 0 | 1.361 ms | 813 µs | 97 Mcycles/s | 89% |

#### Run E2 at 20 MB a second

- time.Now monotonic step: median 0.579 ms, max 70.805 ms (185 steps in 300 ms)
- time.Sleep(1ms) takes: median 1.613 ms, p99 9.629 ms, max 33.094 ms

| | Strategy | Pulses | Median late | p95 | p99 | Max late | Longest gap | Interval jitter (RMS) | > 1 ms | > 5 ms | Missed | Drift | Spin a pulse | CPU | Machine busy |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| a | time.Sleep to the next pulse | 2157 | 0.596 ms | 46.439 ms | 572.883 ms | 1010.37 ms | 1030.78 ms | 25.181 ms | 762 | 442 | 0 | 33.029 ms | 0 µs | 108 Mcycles/s | 96% |
| d | c + MMCSS Pro Audio + TIME_CRITICAL | 2157 | 0.372 ms | 0.618 ms | 0.913 ms | 7.54 ms | 28.04 ms | 0.526 ms | 19 | 5 | 0 | 0.242 ms | 0 µs | 43 Mcycles/s | 93% |
| r | d, aimed early by its recent median lateness | 2157 | 0.001 ms | 0.393 ms | 1.212 ms | 20.54 ms | 41.23 ms | 1.059 ms | 25 | 5 | 0 | 0.213 ms | 0 µs | 45 Mcycles/s | 97% |

- a: 13 collections, 238.26 ms of stop-the-world pauses, the longest 188.70 ms
- d: 11 collections, 22.00 ms of stop-the-world pauses, the longest 17.39 ms
- r: 12 collections, 34.39 ms of stop-the-world pauses, the longest 21.79 ms

#### Run E2 at 100 MB a second

- time.Now monotonic step: median 0.998 ms, max 1.945 ms (345 steps in 300 ms)
- time.Sleep(1ms) takes: median 1.140 ms, p99 1.699 ms, max 4.464 ms

| | Strategy | Pulses | Median late | p95 | p99 | Max late | Longest gap | Interval jitter (RMS) | > 1 ms | > 5 ms | Missed | Drift | Spin a pulse | CPU | Machine busy |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| a | time.Sleep to the next pulse | 2157 | 0.481 ms | 3.805 ms | 13.782 ms | 52.61 ms | 72.69 ms | 3.576 ms | 358 | 76 | 0 | 0.664 ms | 0 µs | 71 Mcycles/s | 93% |
| d | c + MMCSS Pro Audio + TIME_CRITICAL | 2157 | 0.369 ms | 0.621 ms | 0.950 ms | 3.57 ms | 24.14 ms | 0.306 ms | 20 | 0 | 0 | 0.124 ms | 0 µs | 70 Mcycles/s | 84% |
| r | d, aimed early by its recent median lateness | 2157 | 0.004 ms | 0.357 ms | 1.000 ms | 9.47 ms | 30.25 ms | 0.509 ms | 22 | 2 | 0 | 0.282 ms | 0 µs | 70 Mcycles/s | 91% |

- a: 68 collections, 72.81 ms of stop-the-world pauses, the longest 40.77 ms
- d: 67 collections, 28.14 ms of stop-the-world pauses, the longest 21.04 ms
- r: 61 collections, 58.98 ms of stop-the-world pauses, the longest 19.85 ms
