# Research 04: Licensing, Legal Risk and Regulation for "weightkeep"

*Researched 2026-09-24. **This is not legal advice.** It is an engineering and policy survey to help design the tool. Items marked **[UNCERTAIN]** need review by a lawyer, ideally one with open-source and copyright experience (for example via EFF, SFLC or the Software Freedom Conservancy) before launch.*

---

## 0. Key findings

1. **The model's licence decides whether redistribution is allowed, not Hugging Face (HF).** The HF Terms of Service (last updated 2022-09-15) have no explicit anti-scraping or anti-mirroring clause. The licence that public repos grant to other users is limited to use "through our Services and functionalities", so it does **not** give mirror rights. Mirror rights come only from the model's own licence. ([HF ToS](https://huggingface.co/terms-of-service))
2. **Treat gating as a hard stop, whatever the licence tag says.** A gated repo makes every downloader click through an agreement with the author (sharing contact details, and often accepting a licence or acceptable-use policy (AUP)). Re-seeding the files strips that gate away. ([HF gated models docs](https://huggingface.co/docs/hub/en/models-gated))
3. **The `license:` tag is self-declared and often wrong or missing.** One study found only **37.8%** of HF models declare a machine-readable licence, and parent/child licences are often inconsistent ([arXiv 2502.04484](https://arxiv.org/html/2502.04484v2)). Tag checks alone are not enough. The tool must also verify the LICENSE file and the `base_model` lineage.
4. **Why a model was removed matters.** Models do get removed from HF by DMCA or legal complaint: Meta's 2023 LLaMA takedowns, GEITje, datasets such as MATH and AO3. HF publishes these notices in [`huggingface-legal/takedown-notices`](https://huggingface.co/datasets/huggingface-legal/takedown-notices). A tool pitched as "survive removal" must **not** re-seed content removed for legal reasons. Anna's Archive is the cautionary tale: a $322M default judgment and domain seizures in 2026.
5. **As of Sept 2026 there is no US ban on open-weight models.** In Aug 2026 the White House framework explicitly **exempted** open-weight models from pre-release review. However, restrictions on *Chinese* open models are being actively debated, and China is itself weighing export controls on its own open weights. Both are covered in section 4.
6. **Recommended licence for the tool:** Apache-2.0, or dual MIT/Apache-2.0. Put registry metadata under CC0-1.0. Avoid AGPL if adoption and stars matter.

---

## 1. Licence catalogue and tiering

### 1.1 Tier definitions

| Tier | Meaning | Tool behaviour |
|---|---|---|
| **A: Permissive** | OSI/FSF-style licence with a perpetual, irrevocable right to redistribute verbatim copies. Conditions are limited to keeping the licence and notices. | Auto-mirror and auto-seed, but still bundle LICENSE/NOTICE files. |
| **B1: Redistributable with conditions (commercial OK)** | Custom "community" or RAIL-style licence. Redistribution is allowed if you pass on the licence text, a NOTICE string and use restrictions (AUP), and sometimes attribution or naming rules. | Off by default. Seeding requires an explicit per-node opt-in, the full licence and notice bundle, and a click-through notice shown to downloaders. |
| **B2: Redistributable, non-commercial or research only** | Redistribution is allowed, but only for non-commercial or research purposes, or by non-commercial entities. | Opt-in only, never on commercial infrastructure. The node operator must attest that the node is non-commercial. |
| **C: Never auto-seed** | Gated, unknown, missing, "other" without a verified text, licences that forbid redistribution or need individual acceptance, and anything on a takedown or denylist. | Never mirrored. A hash may appear in the index only as a denylist entry. |

### 1.2 Catalogue (HF `license:` identifiers from the [HF licence table](https://huggingface.co/docs/hub/repositories-licenses))

| HF id | Redistribution? | Key conditions | Commercial | Tier |
|---|---|---|---|---|
| `apache-2.0` | Yes | Keep the licence and the NOTICE file, mark modified files. Some vendors layer a usage policy on top (e.g. gpt-oss: [OpenAI](https://help.openai.com/en/articles/11870455-openai-open-weight-models-gpt-oss)). Gemma 4 moved to Apache-2.0 ([Google, Apr 2026](https://opensource.googleblog.com/2026/03/gemma-4-expanding-the-gemmaverse-with-apache-20.html)), as did Qwen3 ([Qwen](https://qwenlm.github.io/blog/qwen3/)). | Yes | **A** |
| `mit` | Yes | Keep the copyright notice and licence. DeepSeek-V3-0324 and R1 are MIT ([SiliconANGLE](https://siliconangle.com/2025/03/24/deepseek-releases-improved-deepseek-v3-model-mit-license/)). | Yes | **A** |
| `bsd-2-clause`, `bsd-3-clause`, `bsd-3-clause-clear`, `isc`, `zlib`, `bsl-1.0`, `postgresql`, `ncsa`, `afl-3.0`, `ecl-2.0`, `ms-pl`, `artistic-2.0` | Yes | Keep the notice. BSD-3 adds a no-endorsement clause. | Yes | **A** |
| `cc0-1.0`, `unlicense`, `pddl`, `wtfpl` | Yes | None | Yes | **A** |
| `openmdw-1.0`, `openmdw-1.1` | Yes | Permissive, built for models; NVIDIA is adopting it ([Linux Foundation](https://www.linuxfoundation.org/press/linux-foundation-releases-openmdw-1.1-nvidia-adopts-openmdw-for-cosmos-isaac-gr00t-ising-and-nemotron-ai-model-families)) | Yes | **A** |
| `cc-by-2.0` through `cc-by-4.0`, `odc-by`, `cdla-permissive-1.0/2.0` | Yes | Attribution: licensor name, licence link, and an indication of any changes | Yes | **A** (the manifest must carry the attribution) |
| `cc-by-sa-3.0/4.0`, `cdla-sharing-1.0`, `odbl` | Yes | Attribution. Share-alike applies to adaptations; verbatim mirroring is fine. | Yes | **A** |
| `mpl-2.0`, `epl-1.0/2.0`, `lgpl-*`, `gpl-*`, `agpl-3.0`, `eupl-1.1/1.2`, `osl-3.0` | Yes | Copyleft source-offer duties. What counts as "source" for weights is unclear **[UNCERTAIN]**. | Yes | **A**, with a note to bundle any source or training code the repo ships |
| `cc-by-nd-4.0` | Verbatim copies only | Attribution, no adaptations. The tool must **never** re-quantise or convert these models. | Yes | **B1** (verbatim-only flag) |
| `openrail`, `bigscience-openrail-m`, `bigscience-bloom-rail-1.0`, `creativeml-openrail-m`, `bigcode-openrail-m`, `openrail++` | Yes | Use restrictions (Attachment A) must be included as **enforceable provisions** in any downstream agreement, and a copy of the licence must be provided ([CreativeML OpenRAIL-M text](https://huggingface.co/spaces/CompVis/stable-diffusion-license/raw/main/license.txt)) | Yes | **B1** |
| `llama2` | Yes | Copy of the agreement; notice "Llama 2 is licensed under the LLAMA 2 Community License…"; AUP; 700M monthly-active-user (MAU) threshold; ban on using outputs to improve other LLMs | Yes, below 700M MAU | **B1** (almost always gated on HF, so C in practice) |
| `llama3`, `llama3.1`, `llama3.2`, `llama3.3`, `llama4` | Yes | (i) Provide a copy of the agreement. (ii) Prominently display **"Built with Llama"** on a related site or docs. (iii) Derivative model names must **start with "Llama"**. (iv) Keep the notice "Llama 4 is licensed under the Llama 4 Community License, Copyright © Meta Platforms, Inc. All Rights Reserved." (v) Comply with the AUP. (vi) 700M MAU threshold. ([Llama 4 licence](https://dev.meta.ai/llama/llama4/license/)). The Llama 3.2 and Llama 4 AUPs **do not grant rights for multimodal models to individuals domiciled in, or companies based in, the EU** ([Llama 4 AUP](https://www.llama.com/llama4/use-policy/), [analysis](https://www.zansara.dev/posts/2025-05-16-llama-eu-ban/)). | Yes, below 700M MAU | **B1**. Official repos are gated (C). Multimodal variants need an EU geo-flag. |
| `gemma` (Gemma 1–3, Gemma Terms of Use) | Yes | Pass on the §3.2 use restrictions as enforceable provisions; give recipients the full terms; mark modifications; include a NOTICE file saying "Gemma is provided under and subject to the Gemma Terms of Use found at ai.google.dev/gemma/terms". Google reserves the right to "restrict (remotely or otherwise)" usage it believes violates the terms. ([Gemma ToU, modified 2026-04-01](https://ai.google.dev/gemma/terms)) | Yes | **B1**. Official repos are gated (C). Gemma 4 is Apache-2.0 (A). |
| `other` + `license_name: qwen` / `tongyi-qianwen` (Qwen 1–2.5 72B etc.) | Yes | Copy of the agreement; notice "Qwen is licensed under the Qwen LICENSE AGREEMENT, Copyright (c) Alibaba Cloud"; "Built with Qwen" for derivatives; separate licence needed above 100M MAU; PRC law, Hangzhou courts ([Qwen2.5-72B LICENSE](https://huggingface.co/Qwen/Qwen2.5-72B-Instruct/blob/main/LICENSE)) | Yes, below 100M MAU | **B1** |
| `other` + `qwen-research` | Research or non-commercial only | Non-commercial | No | **B2** |
| `other` + `deepseek` (DeepSeek License v1: V2, original V3, Coder) | Yes | RAIL-style: pass on the Attachment A use restrictions, provide a copy, keep notices; PRC law ([DeepSeek-V2 LICENSE-MODEL](https://github.com/deepseek-ai/DeepSeek-V2/blob/main/LICENSE-MODEL)) | Yes | **B1** |
| `other` + `mrl` (Mistral AI Research License) | Yes, **but** "any Distribution by a commercial entity… whether in return for payment or free of charge" is excluded from Research Purposes | Copy of the agreement; NOTICE "Licensed by Mistral AI under the Mistral AI Research License" ([MRL](https://mistral.ai/static/licenses/MRL-0.1.md)) | No | **B2**. Commercial node operators must not seed. |
| `other` + `mnpl` (Mistral Non-Production License, e.g. Codestral) | Yes | Copy of the agreement; NOTICE "Licensed by Mistral AI under the Mistral AI Non-Production License"; non-production use only; no supply "in the course of a commercial activity" ([MNPL](https://mistral.ai/licenses/MNPL-0.1.md)) | No | **B2** |
| `other` + `falcon` (TII Falcon License 2.0: Falcon 2, Falcon 3, Mamba) | Yes | Apache-based. AUP must be included as enforceable provisions; attribution "built using… technology from the Technology Innovation Institute"; NOTICE ([TII terms](https://falconllm.tii.ae/falcon-terms-and-conditions.html)) | Yes | **B1** |
| `other` + `falcon-180b` | Yes | As above, plus the **Hosting Use** restriction (no shared inference APIs without permission) must be passed on ([LICENSE](https://huggingface.co/tiiuae/falcon-180B/blob/main/LICENSE.txt)) | Yes, except hosting | **B1** (gated on HF, so C in practice) |
| `other` + `nvidia-open-model-license` | Yes | NOTICE "Licensed by NVIDIA Corporation under the NVIDIA Open Model License"; "Built on NVIDIA Cosmos" for Cosmos models; rights **terminate** if guardrails are bypassed without an equivalent substitute ([NVIDIA OML, 2025-10-24](https://www.nvidia.com/en-us/agreements/enterprise-software/nvidia-open-model-license/)) | Yes | **B1** |
| `other` + `stabilityai-community` (SD3.x, etc.) | Yes | Copy of the agreement; notice; "Powered by Stability AI"; AUP; licence **terminates** for entities with more than US$1M revenue; no use to train other foundation models ([Stability Community License](https://stability.ai/community-license-agreement)) | Yes, below $1M | **B1**, with a warning. Distribution by large companies is unclear **[UNCERTAIN]**. |
| `other` + `flux-1-dev-non-commercial-license` | Yes, for non-commercial purposes | NOTICE "The FLUX.1 [dev] Model is licensed by Black Forest Labs Inc. under the FLUX.1 [dev] Non-Commercial License"; copy of the licence ([LICENSE](https://huggingface.co/black-forest-labs/FLUX.1-dev/blob/main/LICENSE.md)) | No | **B2** (gated on HF, so C in practice) |
| `grok2-community` | Limited | Attribution "Powered by xAI"; no use to train other models; commercial use only under xAI's AUP ([xai-org/grok-2](https://huggingface.co/xai-org/grok-2/blob/main/LICENSE)) | Restricted | **B2** **[UNCERTAIN]** |
| `cc-by-nc-*`, `cc-by-nc-sa-*` (e.g. Cohere Command R, many research models) | Yes, for non-commercial purposes | Attribution; NC; SA for adaptations. Note: often paired with an extra AUP. | No | **B2** |
| `cc-by-nc-nd-*` | Verbatim, non-commercial only | Never transform | No | **B2** (verbatim flag) |
| `fair-noncommercial-research-license`, `apple-amlr`, `h-research`, `intel-research`, `deepfloyd-if-license`, `c-uda`, `lgpl-lr`, `gfdl` | Varies; several require individual acceptance or are research-only with unclear distribution rights | – | No | **C** until a human reviews each licence text. Some may move to B2 later. **[UNCERTAIN]** |
| `unknown`, missing tag, `other` with no `license_name` or LICENSE file | Unknown | Copyright default is "all rights reserved" | – | **C** |
| **Any repo with `gated: auto` or `gated: manual`** | The licence tag is irrelevant | The gate is a contractual precondition to access | – | **C (hard rule)** |
| Any repo or hash in HF takedown notices, a DMCA notice received, or the project denylist | – | – | – | **C (hard rule)** |

**Notes on the "B" licences, which the design must handle:**

- **Accepting the licence binds the distributor.** Most custom licences say that distributing or using the materials means accepting the agreement. Every seeder becomes a licensee, and the licensor may terminate that licence on breach (Llama §6). Seeders must opt in knowingly.
- **AUP passthrough "as enforceable provisions".** OpenRAIL, Gemma, DeepSeek and Falcon all require the redistributor to include the use restrictions in an *agreement* with recipients. A bare magnet link probably does not satisfy this. The tool therefore needs a **click-through licence acceptance in the client** (store a local acceptance record) before B-tier downloads start. **[UNCERTAIN]** whether a CLI prompt counts as an enforceable agreement. Clickwrap is generally enforced in US courts, but that is not guaranteed.
- **"Built with Llama" / "Powered by Stability AI" / "Powered by xAI".** Distributing the materials triggers the display requirement "on a related website, user interface, blogpost, about page, or product documentation". The registry's model page should display it automatically.
- **Naming rules apply to derivatives, not verbatim mirrors.** The tool should never rename, re-quantise or merge B-tier models itself. It should mirror bytes verbatim, verified by hash.
- **Revocable or remote-restrictable terms.** Gemma lets Google "restrict (remotely or otherwise)" usage. Custom licences can be revised (the Gemma ToU was modified 2026-04-01). Apache, MIT and CC licences are irrevocable for copies already released, which is why they are tier A.

### 1.3 Are weights even copyrightable?

**[UNCERTAIN]** Many commentators argue that weights produced by an automated process lack human authorship. This was the argument of the 2023 counter-notice against Meta's llama-dl takedown ([GitHub DMCA counter-notice](https://github.com/github/dmca/blob/master/2023/04/2023-04-27-meta-counternotice.md); [Marble](https://www.marble.onl/posts/model_weight_copyrights.html); [arXiv 2412.07066](https://arxiv.org/pdf/2412.07066)). However, licensors can still sue on contract grounds, and in practice HF and GitHub **comply** with weight takedowns: Meta's 2023 notices took down HF repos and a 403-repo GitHub fork network ([GitHub DMCA 2023-03-21](https://github.com/github/dmca/blob/master/2023/03/2023-03-21-meta.md); [Wikipedia: Llama](https://en.wikipedia.org/wiki/Llama_(language_model))). **Design as if weights are copyrightable and licences are enforceable.**

---

## 2. Gated repos, HF Terms of Service and HF content policy

### What gating means
- A gated repo shows an access request. Clicking "Agree" shares your username and email with the author. Authors can add custom fields and prompts (e.g. "I agree to use this model for non-commercial use ONLY"), approve manually, and "block your access to the model without prior notice" at any time. Downloads need an authenticated token. There is even `extra_gated_eu_disallowed` for licences that forbid EU distribution. ([HF gated models](https://huggingface.co/docs/hub/en/models-gated))
- **Legal effect:** accepting the gate is most likely a **clickwrap contract with the model author**, not with HF. Many gated models (Llama, Gemma 1–3, FLUX-dev, Falcon-180B) put the licence itself in the gate. Some gate prompts forbid redistribution outright, for example "redistribution… strictly prohibited except through Hugging Face" as reported [here](https://theneuralbase.com/huggingface-api/learn/advanced/gated-model-compliance/).
- **Mirroring gated content is not an HF ToS violation as such.** It (a) likely breaches the author's gate terms, (b) defeats the author's access control and contact collection, and (c) signals bad faith, which is fatal in any inducement analysis (see §3). Whether re-seeding counts as "circumvention" under 17 U.S.C. §1201 is **[UNCERTAIN]**; using your own valid token is probably not circumvention, but the breach of contract remains. **Recommendation: hard-skip every gated repo, including auto-approve gates.** Offer a "bring-your-own-token, private cache only, never seed" mode as an escape hatch at most.

### HF Terms of Service (last updated 2022-09-15; [link](https://huggingface.co/terms-of-service))
- **No clause specifically about scraping or crawling** was found. There is a general duty to use the Services "in strict compliance with these Terms, the Supplemental Terms… all of our policies… and all applicable laws". The "may not alter, reproduce, republish, license" clause covers **HF's own proprietary materials** (site, branding), not user repos.
- **Public-repo licence to users:** "If you decide to set your Repository public, you grant each User a perpetual, irrevocable, worldwide, royalty-free, non-exclusive license to use, display, publish, reproduce, distribute, and make derivative works of your Content **through our Services and functionalities**." This is limited to HF's own services, so **it does not authorise off-platform mirroring**. Where a repo carries an open-source licence, that licence governs.
- HF can terminate accounts "with or without cause". Bulk API use should respect rate limits: use `huggingface_hub`, identify the tool in the User-Agent, back off on 429s. Governing law is New York.
- **Also watch:** HF's owner may change. Reports in Aug 2026 said HF hired bankers to explore a sale at roughly $13B ([BERI brief, 2026-08-25](https://www.beri.net/article/hugging-face-sale-model-weight-mirroring-supply-chain-playbook)). That is a legitimate supply-chain-resilience argument for the project. Use it in positioning instead of "get around takedowns".

### HF Content Policy (updated 2025-04-10; [link](https://huggingface.co/content-policy))
- It prohibits illegal content, IP infringement, malware and similar. DMCA notices go to `dmca@huggingface.co` with a counter-notice process. HF may disable access, gate, add a "Not for All Audiences" tag, or unrank content.
- HF publishes takedown notices in a public dataset: [`huggingface-legal/takedown-notices`](https://huggingface.co/datasets/huggingface-legal/takedown-notices). **weightkeep should ingest this feed as a denylist signal.** It should also flag models whose HF repo disappeared, so a human can review the reason before the model is served again.

---

## 3. Legal risk for the tool's authors and registry

### 3.1 The doctrine
- **Sony Betamax (1984):** a technology "capable of substantial non-infringing uses" does not by itself create contributory liability.
- **MGM v. Grokster (2005):** a distributor is liable anyway if it **induces** infringement ("purposeful, culpable expression and conduct"). Marketing, messaging and product design are all evidence. ([EFF](https://www.eff.org/cases/mgm-v-grokster), [Mondaq](https://www.mondaq.com/unitedstates/it-internet/33769/the-supreme-court-decision-in-mgm-v-grokster))
- **Columbia Pictures v. Fung (isoHunt, 9th Cir. 2013):** a **torrent index** was held liable for inducement and **lost the DMCA safe harbour**. Factors included red-flag knowledge, financial benefit plus the ability to control, and actively improving torrents by adding trackers. ([Wikipedia](https://en.wikipedia.org/wiki/Columbia_Pictures_Industries,_Inc._v._Fung), [Loeb](https://www.loeb.com/en/insights/publications/2013/03/columbia-pictures-industries-inc-v-fung)) **This is the closest precedent to a weightkeep registry.**
- **Implication:** the BitTorrent/IPFS **client code** is low risk, since it is generic transfer software with overwhelming lawful use. The **registry**, meaning the curated index of magnet links and hashes, is where the risk sits. Its safety depends on (a) the licence gate, (b) a working takedown process, and (c) never marketing itself as a way to get content that was taken down.

### 3.2 Precedents
| Project | What happened | Lesson |
|---|---|---|
| **youtube-dl** (2020) | The RIAA sent a §1201 DMCA notice and GitHub removed the repo. EFF responded, and GitHub **reinstated** it, created a **$1M developer defence fund**, and changed its policy: §1201 claims now get technical and legal review, and "where we are unable to determine… we will err on the side of the developer" ([GitHub blog](https://github.blog/news-insights/policy-news-and-insights/standing-up-for-developers-youtube-dl-is-back/), [EFF](https://www.eff.org/deeplinks/2020/11/github-reinstates-youtube-dl-after-riaas-abuse-dmca), [GitHub DMCA policy](https://docs.github.com/en/site-policy/content-removal-policies/dmca-takedown-policy)) | General-purpose tools survive, especially with lawful defaults and test fixtures that only use permissive content. youtube-dl's own trouble came from a test file that referenced copyrighted music. **Never** include a gated or restrictive model in tests, docs or demos. |
| **Anna's Archive** (2025–26) | Announced it had scraped about 86M Spotify tracks for BitTorrent distribution. Result: a TRO, Cloudflare and registrar domain suspensions (Jan 2026), a **$322M default judgment** (Apr 2026), and a separate OCLC default judgment ([MBW](https://www.musicbusinessworldwide.com/spotify-and-record-labels-win-322m-default-judgment-against-pirate-site-annas-archive/), [Wikipedia](https://en.wikipedia.org/wiki/Anna%27s_Archive)) | Openly defiant, preservation-branded redistribution of unlicensed content is ruinous. Courts reach infrastructure (domains, CDNs) even when operators are anonymous. |
| **Internet Archive** | Lost *Hachette v. IA* (2d Cir. 2024), where the fair-use defence for controlled digital lending was rejected. Settled the UMG "Great 78" case (Sept 2025, confidential) ([IA blog](https://blog.archive.org/2024/12/04/end-of-hachette-v-internet-archive/), [IA 2025](https://blog.archive.org/2025/09/15/an-update-on-the-great-78s-lawsuit/)) | Even a respected non-profit archive cannot rely on "preservation" as a defence when the licence does not allow it. |
| **Academic Torrents** | A 501(c)(3) BitTorrent index for research data, operated by the Institute for Reproducible Research ([Wikipedia](https://en.wikipedia.org/wiki/Academic_Torrents)) | A non-profit structure plus a curated, licence-aware index is the model to copy. |
| **Kiwix / openZIM** | Redistributes CC BY-SA content. **Removed WikiHow ZIMs when WikiHow asked** in late 2024, even though the licence may have allowed distribution: "our goal is not to get in the way or harm content creators" ([Kiwix](https://hub.kiwix.org/weblog/2025/1/wikihow-content-has-been-deprecated/)) | Honour author opt-out requests as a courtesy, even for irrevocable licences. It builds trust and reduces conflict. |
| **Meta vs. LLaMA mirrors** (2023) | DMCA notices removed HF repos and a 403-repo GitHub fork network ([GitHub DMCA](https://github.com/github/dmca/blob/master/2023/03/2023-03-21-meta.md)) | Weight owners do use the DMCA, and platforms comply. |

### 3.3 What the registry should do
1. **Index hashes and metadata only. Never host weights on project infrastructure** for B or C tiers. Seeding by volunteers is opt-in and happens on their own nodes.
2. **Make licence fields mandatory:** SPDX expression, HF licence id, SHA-256 of the licence text, source repo URL plus commit SHA, and `gated` status at snapshot time. Unrecognised values default to C.
3. **Register a DMCA agent** with the US Copyright Office (US$6, renew **every 3 years**; a lapse loses the safe harbour for that gap) to qualify for the §512(d) "information location tools" safe harbour ([Copyright Office FAQ](https://www.copyright.gov/dmca-directory/faq.html), [NatLawReview](https://natlawreview.com/article/no-time-right-time-to-update-your-dmca-safe-harbor-copyright-agent-registration)). Publish a `/dmca` page, a counter-notice process, and a **repeat-infringer policy** for submitters. **[UNCERTAIN]** An unincorporated project may struggle to use the safe harbour; consider a fiscal host or foundation (OpenCollective, Software Freedom Conservancy) or a small non-profit, following the Academic Torrents model.
4. **Keep a signed, append-only denylist** that every client obeys: hashes, repo ids and licence ids. Clients refuse to seed or download denylisted hashes. Feed it from DMCA notices received, HF takedown-notices, author opt-out requests, and legal or jurisdictional flags.
5. **Take down quickly.** Remove a manifest within 24–72 hours of a facially valid notice, and publish takedowns transparently (as GitHub does with `github/dmca`).
6. **Avoid inducement signals.** Do not use the tagline "models that were taken down". Do not index gated or removed-for-cause models. Do not add trackers to third-party content (the isoHunt factor). Do not run "most wanted" lists of unavailable models. Do not have revenue tied to traffic.
7. **Honour author opt-out even for tier A**, with a documented process, as Kiwix did.

---

## 4. Export controls and regulation (status as of 2026-09-24)

### 4.1 United States
- **AI Diffusion Rule (Jan 2025)** created ECCN **4E091** for closed weights of models trained with more than 10^26 operations. It **explicitly excluded published weights**: "ECCN 4E091 does not control the model weights of any AI model that has been 'published' as defined in 734.7(a)" ([WilmerHale](https://www.wilmerhale.com/en/insights/publications/20250205-bis-issues-long-awaited-export-controls-on-ai), [Federal Register](https://www.federalregister.gov/documents/2025/01/15/2025-00636/framework-for-artificial-intelligence-diffusion)).
- **Rescission:** on 2025-05-13 BIS announced it would rescind the rule and **not enforce** it (the compliance date was 2025-05-15), with a replacement rule promised ([Kirkland](https://www.kirkland.com/publications/kirkland-alert/2025/05/bis-rescission-of-the-biden-administration)). **[UNCERTAIN / conflicting sources]** Some 2026 practitioner guides say the 4E091 text technically remains in the CFR, unenforced and contested ([One Lex](https://www.onelexpartners.com/news-and-insights/us-export-controls-and-ai-a-practitioners-guide)). Either way, **public release of open weights has been treated as outside licensing requirements.**
- **June 2026:** BIS used an "is-informed" letter to make Anthropic suspend foreign-national access to its closed Fable 5 / Mythos 5 models. This was the first use of export authority against a deployed frontier model's API ([CSA](https://labs.cloudsecurityalliance.org/research/csa-research-note-ai-model-export-controls-20260618-csa-styl/)). It shows the authority exists and can move within days, but it targeted a **closed** model.
- **June 2026 executive order and Aug 4, 2026 framework:** a voluntary pre-release security review administered by CAISI/NIST for frontier **closed** models. **Open-weight models are exempt** ([Washington Post](https://www.washingtonpost.com/technology/2026/08/04/white-house-will-exempt-open-ai-systems-security-review/), [Axios](https://www.axios.com/2026/08/04/trump-ai-framework-open-models), [Quartz](https://qz.com/white-house-open-weight-ai-models-exempt-security-review-080526)).
- **Pressure on Chinese open models:** the administration has considered banning Chinese open-weight models and sanctioning firms over "distillation" (the Kimi K3 allegation). On **2026-07-24**, 25 companies and groups (Nvidia, Microsoft, Meta, Mistral, IBM, HF, Mozilla, the Linux Foundation and others) published "Open Weights and American AI Leadership" against broad restrictions ([TechCrunch](https://techcrunch.com/2026/07/24/as-us-weighs-response-to-chinese-ai-industry-urges-against-broad-open-weight-restrictions/)). Nathan Lambert warned on 2026-07-12 of a possible capability-threshold ban or delay within about 6 months ([Interconnects](https://www.interconnects.ai/p/6-months-to-live-for-open-models)).
- **Enacted restrictions so far are narrow:** DeepSeek bans on government devices in several states; the FY2026 NDAA excludes DeepSeek and High-Flyer AI from DoD systems and contractors. Pending bills include H.R.1121 ("No DeepSeek on Government Devices Act") and the Cassidy–Rosen adversarial-model list ([Commercient summary](https://www.commercient.com/us-ban-open-weight-ai-models-2/)).
- **Verdict on the claimed "US ban on open-source models" in 2026: FALSE as of 2026-09-24.** There is no federal law, executive order or export control that bans public distribution or use of open-weight models. Polymarket priced about **14%** for a federal ban on any open model in 2026 ([Polymarket](https://polymarket.com/event/us-government-bans-an-open-source-ai-model-in-2026-20260703221501747)). **The risk is still real, and most likely to target Chinese-origin models.** weightkeep should be able to apply a jurisdictional denylist quickly, for example `origin: CN` for US nodes, if one arrives.
- **Federal preemption:** the EO of 2025-12-11 ("Ensuring a National Policy Framework for AI") created a DOJ AI Litigation Task Force to challenge state AI laws ([White House](https://www.whitehouse.gov/presidential-actions/2025/12/eliminating-state-law-obstruction-of-national-artificial-intelligence-policy/), [Sidley](https://www.sidley.com/en/insights/newsupdates/2025/12/unpacking-the-december-11-2025-executive-order)).

### 4.2 US states
- **California SB 1047** was vetoed on 2024-09-29.
- **California SB 53 (TFAIA)** was signed on 2025-09-29 and took effect on 2026-01-01. It applies to *frontier developers* (more than 10^26 FLOP), with more duties for *large* ones (over $500M revenue): safety frameworks, transparency reports and incident reporting. It dropped SB 1047's kill-switch and does not hold developers responsible for controlling models after release. **It places no duties on redistributors** ([FPF](https://fpf.org/blog/californias-sb-53-the-first-frontier-ai-law-explained/), [Mayer Brown](https://www.mayerbrown.com/en/insights/publications/2025/10/california-enacts-sb-53-creating-new-requirements-for-developers-of-frontier-artificial-intelligence-models-and-related-whistleblower-provisions)).

### 4.3 European Union (AI Act)
- GPAI obligations have applied since **2025-08-02**. The AI Office gained enforcement powers on **2026-08-02**. Models placed on the market before Aug 2025 must comply by **2027-08-02**. The Code of Practice was final on 2025-07-10 ([Latham](https://www.lw.com/en/insights/eu-ai-act-gpai-model-obligations-in-force-and-final-gpai-code-of-practice-in-place), [Skadden](https://www.skadden.com/insights/publications/2025/08/eus-general-purpose-ai-obligations)).
- **Open-source exemption:** it applies to models under a free and open-source licence that allows use, modification and redistribution, with no monetisation and public parameters. Such models are exempt from the technical documentation duties but **must still** publish a training-data summary and keep a copyright policy. **No exemption** applies to systemic-risk models (10^25 FLOP or more) ([AI Act GPAI guidelines overview](https://artificialintelligenceact.eu/gpai-guidelines-overview/)).
- **Mirrors are not providers:** the Commission guidelines say uploading to or hosting a repository **does not transfer provider status**. Only modifiers using more than one third of the original training compute become providers. weightkeep, which only redistributes verbatim, is very likely **not** a GPAI provider. **[UNCERTAIN]** for fine-tunes submitted by users.
- **Digital Omnibus on AI** (Reg. (EU) 2026/1744, in force 2026-07-27) delayed the **high-risk** deadlines to Dec 2027 and Aug 2028. **GPAI rules are unchanged** ([White & Case](https://www.whitecase.com/insight-alert/eu-ai-omnibus-enters-force-amending-ai-act), [Cooley](https://cdp.cooley.com/digital-ai-omnibus-delays-key-deadlines-introduces-new-rules/)).
- **Licence-level EU issue:** the Llama 3.2 and Llama 4 multimodal AUPs withhold rights from EU-domiciled persons. EU node operators should not seed those models.

### 4.4 China
- China already has export-control catalogues and the generative AI Interim Measures (2023). In **July 2026**, MOFCOM began consulting Alibaba, ByteDance and Zhipu on whether to restrict **foreign downloads of Chinese open weights** (including Qwen) and cross-border transfer of training data, through the next revision of the prohibited/restricted technology export catalogue. **Nothing had been decided** at the time of reporting ([Quartz](https://qz.com/beijing-china-ai-model-export-restrictions-070726), [TrendingTopics](https://www.trendingtopics.eu/china-weighs-export-controls-on-ai-models-including-open-weight-llms/), [TechTimes](https://www.techtimes.com/articles/321270/20260722/china-weighs-locking-ai-model-weights-download-what-you-use-right-now.htm)).
- **Implication:** Chinese export rules would bind Chinese firms, not foreign mirrors. Already-released Apache and MIT weights stay licensed. However, **future** Qwen and DeepSeek releases could switch to restrictive terms. The Qwen licence and the DeepSeek v1 licence both choose PRC law and Hangzhou courts.

---

## 5. Policy recommendations for weightkeep

### 5.1 Machine-readable licence policy (`policy/licenses.yaml`)
Use SPDX IDs where they exist. SPDX does not cover most model licences (the BigScience-OpenRAIL-M request was not accepted: [spdx/license-list-XML#1622](https://github.com/spdx/license-list-XML/issues/1622)), so use `LicenseRef-` IDs keyed to the **SHA-256 of the canonical licence text**.

```yaml
# policy/licenses.yaml: evaluated by the client AND the registry CI
version: 1
default_tier: C                    # anything unmatched is never seeded
hard_rules:                        # checked before licence lookup
  - if: {hf.gated: [auto, manual, true]}          then: C
  - if: {hf.private: true}                        then: C
  - if: {denylist.hit: true}                      then: C
  - if: {license_file.present: false}             then: C
  - if: {license_file.sha256_matches_declared: false} then: C   # tag says MIT, file says Llama
  - if: {base_model.tier: more_restrictive}       then: inherit_base   # lineage check
tiers:
  A:
    spdx: [Apache-2.0, MIT, BSD-2-Clause, BSD-3-Clause, BSD-3-Clause-Clear, ISC, Zlib,
           BSL-1.0, PostgreSQL, NCSA, CC0-1.0, Unlicense, PDDL-1.0, CC-BY-4.0, CC-BY-3.0,
           CC-BY-SA-4.0, CC-BY-SA-3.0, ODC-By-1.0, CDLA-Permissive-2.0, OpenMDW-1.1*, MPL-2.0]
    requires: [bundle_license_file, bundle_notice_file, attribution_in_manifest]
  B1:
    ids: [LicenseRef-Llama-3.1, LicenseRef-Llama-3.2, LicenseRef-Llama-3.3, LicenseRef-Llama-4,
          LicenseRef-Gemma-ToU, LicenseRef-OpenRAIL-M, LicenseRef-CreativeML-OpenRAIL-M,
          LicenseRef-OpenRAIL-PlusPlus, LicenseRef-BigCode-OpenRAIL-M, LicenseRef-Qwen,
          LicenseRef-DeepSeek-1.0, LicenseRef-TII-Falcon-2.0, LicenseRef-NVIDIA-OML,
          LicenseRef-Stability-Community, CC-BY-ND-4.0]
    requires: [node_opt_in, downloader_clickwrap, bundle_license_file, bundle_notice_file,
               bundle_aup, display_attribution_string, verbatim_only]
    geo_block: {LicenseRef-Llama-3.2-multimodal: [EU], LicenseRef-Llama-4-multimodal: [EU]}
  B2:
    ids: [CC-BY-NC-4.0, CC-BY-NC-SA-4.0, CC-BY-NC-ND-4.0, LicenseRef-Mistral-MRL,
          LicenseRef-Mistral-MNPL, LicenseRef-Qwen-Research, LicenseRef-FLUX-1-dev-NC,
          LicenseRef-Grok-2-Community]
    requires: [B1.requires, node_attests_noncommercial, never_on_commercial_infra]
```
*(\* Check whether OpenMDW has received an SPDX ID; otherwise use a LicenseRef.)*

Policy changes need 2 maintainer approvals and a changelog entry. Each release of the policy file is signed.

### 5.2 Manifest requirements (per model snapshot)
Required fields: `source.repo`, `source.commit_sha`, `source.snapshot_time`, `hf.license`, `hf.license_name`, `hf.license_link`, `hf.gated` (at snapshot time), `spdx_expression`, `tier`, `license_file.path` and `license_file.sha256`, `notice_file` (generated for B tiers with the exact vendor notice string), `aup_file` (B tiers), `attribution_string` (for example "Built with Llama"), `base_model` lineage and resolved tiers, `origin_country` (for jurisdictional filtering), per-file SHA-256 and piece hashes, and `removal_status` (last HF check, with the reason if known).
**The LICENSE file must be inside the torrent or IPFS content**, not only in the manifest.

### 5.3 README disclaimer (draft wording)
> **Licences and legal notice.** weightkeep is general-purpose software for verifying, mirroring and sharing files over BitTorrent, IPFS and HTTP mirrors. By default it only mirrors models whose declared licence explicitly allows redistribution. It never mirrors gated, private, unlicensed or denylisted repositories. Each model remains under **its own licence**, which is included with every copy. You, the operator of a node, are responsible for complying with that licence and with the laws that apply to you, including export controls and sanctions. Some licences (for example the Llama, Gemma, OpenRAIL, Mistral Research and CC BY-NC licences) impose use restrictions or non-commercial conditions. These are **disabled by default** and require explicit opt-in. weightkeep does not grant any rights in any model, and the maintainers do not review models for legality, safety or fitness. The software is provided "AS IS" under the Apache License 2.0. **This is not legal advice.** Rights holders can request removal at `/DMCA.md`. We honour valid notices and model authors' opt-out requests.

### 5.4 CONTRIBUTING and registry-submission rules
- Submissions must come through a PR or a signed API call from a verified account, include the manifest, and pass CI. CI re-fetches the HF metadata, checks the gate status and licence file hash, walks the lineage, and checks the denylist.
- Contributors attest (DCO sign-off) that the model is **publicly and ungated** available under the declared licence, and that they are not submitting leaked, removed-for-cause, or personal-data-bearing models.
- **No** models that were removed from HF in response to a legal complaint. **No** re-uploads of gated models by third parties, for example ungated Llama copies from other orgs. These are flagged for human review.
- Quantised or converted derivatives are only accepted if the upstream licence allows adaptations (never for `*-ND`), and must keep the upstream licence and naming rules ("Llama…" prefix).
- Repeat-infringer policy: three substantiated notices and the submitter is banned.
- Tests, docs and demos may use **tier-A models only**, to avoid the youtube-dl test-fixture trap.

### 5.5 Code of conduct and governance
- Adopt **Contributor Covenant 2.1**, and add a "Lawful Use" section: no piracy, no evasion of takedowns, respect for author opt-outs.
- Maintain `SECURITY.md`, `DMCA.md` (agent contact, notice and counter-notice format, response SLA), `TAKEDOWNS/` (a public log like `github/dmca`), and `GOVERNANCE.md`.
- Consider a fiscal host or foundation (OpenCollective Europe, Software Freedom Conservancy, or the Linux Foundation / LF AI & Data, which already sponsors OpenMDW). A foundation also helps with DMCA-agent registration and liability.
- **Positioning:** "supply-chain resilience and preservation of *openly licensed* models". This fits with pinning, mirroring and the HF sale news. Avoid "uncensorable" or "survive takedowns" language (the Grokster and isoHunt risk).

### 5.6 Licence for the tool itself
| Option | Adoption and stars | Fit |
|---|---|---|
| **MIT** | The most popular licence (OSI 2025 top list: MIT #1 at about 4x Apache pageviews: [OSI](https://opensource.org/blog/top-open-source-licenses-in-2025)). Minimal friction. | No patent grant. |
| **Apache-2.0** | The #2 licence, and the norm in the ML ecosystem (transformers, huggingface_hub, vLLM, most model licences). Enterprise-friendly. | **Explicit patent grant and NOTICE mechanism.** It matches the tier-A content the tool promotes. **Recommended.** |
| **Dual MIT OR Apache-2.0** | Rust ecosystem convention. Maximum compatibility, including GPLv2. | Good if the core is written in Rust. |
| GPL-3.0 / AGPL-3.0 | Noticeably fewer corporate contributors and adopters. AGPL is on many company deny-lists. | Only worth it if you want to stop proprietary SaaS forks. Not relevant to legal protection. |

**Recommendation:** use **Apache-2.0**, or `MIT OR Apache-2.0` if the core is Rust, for the code. Use **CC0-1.0** for registry metadata and manifests (they are factual, and CC0 maximises reuse). State clearly that **model weights are not covered** by either licence. The licence choice has no effect on DMCA or inducement exposure; conduct and process decide that.

---

## 6. Open questions for counsel **[UNCERTAIN]**
1. Is a CLI click-through enough to satisfy the "include use restrictions as enforceable provisions" clauses in OpenRAIL, Gemma, DeepSeek and Falcon?
2. Can an unincorporated open-source registry claim §512(d) safe harbour, and should it incorporate or join a fiscal host first?
3. Does seeding a CC-BY-NC or MRL model from a volunteer node run by an employee of a company count as distribution "by a commercial entity"?
4. How should the project respond if a licensor revises a custom licence (Gemma ToU-style) after a snapshot?
5. What is the exposure under Chinese law (PRC-law clauses) and under any future US measures aimed at Chinese-origin weights?
6. Do DMCA §1201 anti-circumvention rules apply if a user's token is used to fetch gated weights for a private cache? (The recommendation is not to support this at all in v1.)
