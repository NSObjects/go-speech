# Natural Mandarin ASR test candidates (Issue #49)

This candidate test is separate from the strict PCM/Ogg/raw Opus 4-turn and
12-turn streaming gates. Those currently repeat the same 2.39-second natural
Mandarin phrase. Replacing that fixture before validating other clips risks
misclassifying synthetic/poor-quality audio as a transport failure.

Two **different, naturally spoken** Mandarin examples are from
[Google FLEURS](https://huggingface.co/datasets/google/fleurs),
`cmn_hans_cn` test split, **CC BY 4.0**.

| Clip | Original FLEURS ID | Reference transcript | WAV Git blob SHA | Source |
|---|---|---|---|---|
| zh-short.wav (11.00 s) | 17639585860488007329 | 日本原子能机构表示，核电站检测出了放射性物质铯和碘。 | d21d2aea863f71e94d3e55eda53190a931da9318 | transcribe.cpp samples |
| fleurs-zh.wav (10.38 s) | 1906 | “这并不是告别。这是一个篇章的结束，也是新篇章的开始。” | 8f0660a5fd620ab2f3f005e8d29641619086a2b8 | transcribe.cpp samples |

Attribution: Google FLEURS project and original speech contributors, via
`handy-computer/transcribe.cpp`
[commit adfc502](https://github.com/handy-computer/transcribe.cpp/tree/adfc50210a3f026a655a3e19937d90ef2bf0728c).
The [upstream sample README](https://github.com/handy-computer/transcribe.cpp/blob/adfc50210a3f026a655a3e19937d90ef2bf0728c/samples/README.md)
identifies source split, FLEURS IDs, speech format, and CC BY 4.0 license;
reference sentences are from the
[official FLEURS test TSV](https://huggingface.co/datasets/google/fleurs/blob/main/data/cmn_hans_cn/test.tsv)
(the first clip) and
[FLEURS row 1906](https://evalscope.readthedocs.io/en/latest/benchmarks/fleurs.html)
(the second clip).

The paid workflow downloads the pinned WAV files without committing them to
this repository, verifies Git blob hashes, then converts them **outside**
the SDK to 16 kHz mono signed PCM16LE. The canary tests both result modes and
requires independently selected keywords from each human transcript, plus
stable EOF. Test audio and transcript are public, with no user/device data.
No transport implementation or current strict regression assertion is changed.

**Status:** these clips are candidates, not yet approved replacements. The
multiple-turn natural-phrase coverage remains incomplete until this canary and
a follow-up multi-turn A/B build both pass real cloud CI.
