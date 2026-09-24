package policy

// The licence table. Sources and the reasoning per licence are in
// .claude/skills/licence-policy/reference.md; the tiers are ADR 0007.
//
// Fingerprints are phrases that must all appear (case and whitespace
// insensitive) in a LICENSE file for it to count as that licence. They are
// deliberately short, so a text with a different copyright line or light
// formatting changes still matches.

type licence struct {
	hfID   string // the Hub's `license:` value (or license_name for "other")
	spdx   string // SPDX id when there is one; bundled text for tier A
	name   string
	tier   Tier
	prints []string
	// bundled means weightkeep ships the text (texts/<spdx>.txt).
	bundled bool
	note    string
}

var licences = []licence{
	// Tier A: permissive; LICENSE and NOTICE travel with the content.
	{hfID: "apache-2.0", spdx: "Apache-2.0", name: "Apache License 2.0", tier: A, bundled: true, prints: []string{"apache license", "version 2.0"}},
	{hfID: "mit", spdx: "MIT", name: "MIT License", tier: A, bundled: true, prints: []string{"permission is hereby granted, free of charge"}},
	{hfID: "bsd-2-clause", spdx: "BSD-2-Clause", name: "BSD 2-Clause", tier: A, bundled: true, prints: []string{"redistribution and use in source and binary forms"}},
	{hfID: "bsd-3-clause", spdx: "BSD-3-Clause", name: "BSD 3-Clause", tier: A, bundled: true, prints: []string{"redistribution and use in source and binary forms", "neither the name"}},
	{hfID: "bsd-3-clause-clear", spdx: "BSD-3-Clause-Clear", name: "BSD 3-Clause Clear", tier: A, bundled: true, prints: []string{"redistribution and use in source and binary forms", "no express or implied licenses to any party's patent rights"}},
	{hfID: "isc", spdx: "ISC", name: "ISC License", tier: A, bundled: true, prints: []string{"permission to use, copy, modify, and/or distribute"}},
	{hfID: "zlib", spdx: "Zlib", name: "zlib License", tier: A, bundled: true, prints: []string{"this software is provided 'as-is'"}},
	{hfID: "bsl-1.0", spdx: "BSL-1.0", name: "Boost Software License 1.0", tier: A, bundled: true, prints: []string{"boost software license"}},
	{hfID: "cc0-1.0", spdx: "CC0-1.0", name: "CC0 1.0", tier: A, bundled: true, prints: []string{"cc0 1.0"}},
	{hfID: "unlicense", spdx: "Unlicense", name: "The Unlicense", tier: A, bundled: true, prints: []string{"free and unencumbered software released into the public domain"}},
	{hfID: "wtfpl", spdx: "WTFPL", name: "WTFPL", tier: A, bundled: true, prints: []string{"do what the fuck you want to"}},
	{hfID: "pddl", spdx: "PDDL-1.0", name: "ODC Public Domain Dedication", tier: A, bundled: true, prints: []string{"public domain dedication"}},
	{hfID: "cc-by-3.0", spdx: "CC-BY-3.0", name: "CC BY 3.0", tier: A, bundled: true, prints: []string{"attribution 3.0"}},
	{hfID: "cc-by-4.0", spdx: "CC-BY-4.0", name: "CC BY 4.0", tier: A, bundled: true, prints: []string{"attribution 4.0 international"}},
	{hfID: "cc-by-sa-3.0", spdx: "CC-BY-SA-3.0", name: "CC BY-SA 3.0", tier: A, bundled: true, prints: []string{"attribution-sharealike 3.0"}},
	{hfID: "cc-by-sa-4.0", spdx: "CC-BY-SA-4.0", name: "CC BY-SA 4.0", tier: A, bundled: true, prints: []string{"attribution-sharealike 4.0"}},
	{hfID: "odc-by", spdx: "ODC-By-1.0", name: "ODC Attribution 1.0", tier: A, bundled: true, prints: []string{"open data commons attribution license"}},
	{hfID: "cdla-permissive-1.0", spdx: "CDLA-Permissive-1.0", name: "CDLA Permissive 1.0", tier: A, bundled: true, prints: []string{"community data license agreement"}},
	{hfID: "cdla-permissive-2.0", spdx: "CDLA-Permissive-2.0", name: "CDLA Permissive 2.0", tier: A, bundled: true, prints: []string{"community data license agreement"}},
	{hfID: "mpl-2.0", spdx: "MPL-2.0", name: "Mozilla Public License 2.0", tier: A, bundled: true, prints: []string{"mozilla public license"}},
	{hfID: "postgresql", spdx: "PostgreSQL", name: "PostgreSQL License", tier: A, bundled: true, prints: []string{"permission to use, copy, modify, and distribute"}},
	{hfID: "ncsa", spdx: "NCSA", name: "University of Illinois/NCSA", tier: A, bundled: true, prints: []string{"university of illinois"}},
	{hfID: "afl-3.0", spdx: "AFL-3.0", name: "Academic Free License 3.0", tier: A, bundled: true, prints: []string{"academic free license"}},
	{hfID: "ecl-2.0", spdx: "ECL-2.0", name: "Educational Community License 2.0", tier: A, bundled: true, prints: []string{"educational community license"}},
	{hfID: "ms-pl", spdx: "MS-PL", name: "Microsoft Public License", tier: A, bundled: true, prints: []string{"microsoft public license"}},
	{hfID: "artistic-2.0", spdx: "Artistic-2.0", name: "Artistic License 2.0", tier: A, bundled: true, prints: []string{"artistic license 2.0"}},
	{hfID: "openmdw-1.0", spdx: "OpenMDW-1.0", name: "OpenMDW 1.0", tier: A, bundled: true, prints: []string{"openmdw"}},
	{hfID: "openmdw-1.1", name: "OpenMDW 1.1", tier: A, prints: []string{"openmdw"}, note: "no SPDX text yet; the repo must ship its own"},

	// Tier B1: redistribution allowed with conditions (licence copy, NOTICE,
	// passing on use restrictions, naming rules). Opt-in per model.
	{hfID: "llama2", name: "Llama 2 Community License", tier: B1, prints: []string{"llama 2 community license"}},
	{hfID: "llama3", name: "Meta Llama 3 Community License", tier: B1, prints: []string{"llama 3 community license"}},
	{hfID: "llama3.1", name: "Llama 3.1 Community License", tier: B1, prints: []string{"llama 3.1 community license"}},
	{hfID: "llama3.2", name: "Llama 3.2 Community License", tier: B1, prints: []string{"llama 3.2 community license"}, note: "no rights for multimodal models to people and companies in the EU"},
	{hfID: "llama3.3", name: "Llama 3.3 Community License", tier: B1, prints: []string{"llama 3.3 community license"}},
	{hfID: "llama4", name: "Llama 4 Community License", tier: B1, prints: []string{"llama 4 community license"}, note: "no rights for multimodal models to people and companies in the EU"},
	{hfID: "gemma", name: "Gemma Terms of Use", tier: B1, prints: []string{"gemma"}},
	{hfID: "openrail", name: "OpenRAIL", tier: B1, prints: []string{"rail"}},
	{hfID: "openrail++", name: "OpenRAIL++-M", tier: B1, prints: []string{"rail"}},
	{hfID: "creativeml-openrail-m", name: "CreativeML OpenRAIL-M", tier: B1, prints: []string{"openrail"}},
	{hfID: "bigscience-openrail-m", name: "BigScience OpenRAIL-M", tier: B1, prints: []string{"openrail"}},
	{hfID: "bigscience-bloom-rail-1.0", name: "BigScience BLOOM RAIL 1.0", tier: B1, prints: []string{"rail"}},
	{hfID: "bigcode-openrail-m", name: "BigCode OpenRAIL-M", tier: B1, prints: []string{"openrail"}},
	{hfID: "cc-by-nd-4.0", spdx: "CC-BY-ND-4.0", name: "CC BY-ND 4.0", tier: B1, prints: []string{"noderivatives 4.0"}, note: "verbatim copies only"},
	{hfID: "qwen", name: "Qwen License Agreement", tier: B1, prints: []string{"qwen"}},
	{hfID: "tongyi-qianwen", name: "Tongyi Qianwen License Agreement", tier: B1, prints: []string{"tongyi qianwen"}},
	{hfID: "deepseek", name: "DeepSeek License Agreement", tier: B1, prints: []string{"deepseek"}},
	{hfID: "falcon", name: "TII Falcon License 2.0", tier: B1, prints: []string{"falcon"}},
	{hfID: "nvidia-open-model-license", name: "NVIDIA Open Model License", tier: B1, prints: []string{"nvidia open model license"}},
	{hfID: "stabilityai-community", name: "Stability AI Community License", tier: B1, prints: []string{"stability ai community license"}},
	// Copyleft: redistribution is allowed, but what "source" means for
	// weights is unsettled. Opt-in until that's clearer.
	{hfID: "gpl-2.0", spdx: "GPL-2.0-only", name: "GNU GPL 2.0", tier: B1, prints: []string{"gnu general public license"}},
	{hfID: "gpl-3.0", spdx: "GPL-3.0-only", name: "GNU GPL 3.0", tier: B1, prints: []string{"gnu general public license"}},
	{hfID: "lgpl-3.0", spdx: "LGPL-3.0-only", name: "GNU LGPL 3.0", tier: B1, prints: []string{"gnu lesser general public license"}},
	{hfID: "agpl-3.0", spdx: "AGPL-3.0-only", name: "GNU AGPL 3.0", tier: B1, prints: []string{"gnu affero general public license"}},

	// Tier B2: redistribution only for non-commercial or research purposes.
	{hfID: "cc-by-nc-2.0", spdx: "CC-BY-NC-2.0", name: "CC BY-NC 2.0", tier: B2, prints: []string{"noncommercial"}},
	{hfID: "cc-by-nc-3.0", spdx: "CC-BY-NC-3.0", name: "CC BY-NC 3.0", tier: B2, prints: []string{"noncommercial 3.0"}},
	{hfID: "cc-by-nc-4.0", spdx: "CC-BY-NC-4.0", name: "CC BY-NC 4.0", tier: B2, prints: []string{"noncommercial 4.0"}},
	{hfID: "cc-by-nc-sa-4.0", spdx: "CC-BY-NC-SA-4.0", name: "CC BY-NC-SA 4.0", tier: B2, prints: []string{"noncommercial-sharealike 4.0"}},
	{hfID: "cc-by-nc-nd-4.0", spdx: "CC-BY-NC-ND-4.0", name: "CC BY-NC-ND 4.0", tier: B2, prints: []string{"noncommercial-noderivatives 4.0"}, note: "verbatim copies only"},
	{hfID: "mrl", name: "Mistral AI Research License", tier: B2, prints: []string{"mistral ai research license"}, note: "no distribution by commercial entities"},
	{hfID: "mnpl", name: "Mistral AI Non-Production License", tier: B2, prints: []string{"non-production license"}},
	{hfID: "flux-1-dev-non-commercial-license", name: "FLUX.1 [dev] Non-Commercial License", tier: B2, prints: []string{"non-commercial license"}},
	{hfID: "qwen-research", name: "Qwen Research License", tier: B2, prints: []string{"qwen research license"}},
}
