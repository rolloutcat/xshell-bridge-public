# Third-party sources and license notices

This independently developed project's source code is licensed under the [MIT License](LICENSE). Third-party components remain under their own licenses. Distributors must retain the project license and the bundled third-party notices.

We thank the OpsKat project and its contributors for publishing their operations and approval designs. Specific inspiration and implementation differences are documented in the [OpsKat acknowledgment and design comparison](docs/opskat-comparison.md).

| Source | Type / use | License / disposition |
|---|---|---|
| OpsKat contributors — https://github.com/opskat/opskat | GPLv3 source used as behavior reference only; see the [pinned comparison](docs/opskat-comparison.md) | Official [GPLv3 LICENSE at 4a218eb05241a2f57622c13d9ec517771aca3b4d](https://github.com/opskat/opskat/blob/4a218eb05241a2f57622c13d9ec517771aca3b4d/LICENSE). No code or assets copied, no new dependency; OpsKat is not a runtime dependency. This does not relicense this independent project. |
| NetSarang Xshell — https://www.netsarang.com/ | Separately installed proprietary application; official scripting API interoperability | Xshell remains subject to its vendor license, not bundled or relicensed here. Session API: https://netsarang.atlassian.net/wiki/spaces/ENSUP/pages/2237305704/xsh.Session ; Screen API: https://netsarang.atlassian.net/wiki/spaces/ENSUP/pages/2237305722/xsh.Screen |
| Microsoft WebView2 SDK — https://www.nuget.org/packages/Microsoft.Web.WebView2 | Distributed SDK assemblies and loader | Actual SDK LICENSE.txt copied as WebView2-LICENSE.txt by build-desktop.ps1. |
| Microsoft WebView2 Fixed Version Runtime — https://developer.microsoft.com/en-us/microsoft-edge/webview2/ | Full portable bundle browser runtime | Separate Microsoft runtime terms apply; SDK license is not a substitute. The full bundle includes licenses/webview2-fixed-runtime/WebView2-Fixed-Runtime.html and source.txt, copied from the repository license records. Terms were fetched on 2026-11-05 from the official endpoint https://developer.microsoft.com/microsoft-edge/api/eula/webview2?locale=en-us&fixed=true . Preserve the runtime unchanged, including Widevine and TrustProtection license lists and LICENSE files. Embedded third-party credits can be displayed using the vendor-provided show_third_party_software_licenses.bat. Exact runtime download URL is included in WebView2-Runtime-source.txt. Distribution: https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution |
| Go toolchain — https://go.dev/ | Compiled runtime/toolchain notice | Actual GOROOT/LICENSE copied to licenses/go/LICENSE. |
| mvdan.cc/sh/v3 v3.12.0 — https://github.com/mvdan/sh | Direct Shell syntax and literal-word parsing dependency; no shell interpreter is executed | BSD-3-Clause; actual LICENSE retained by collect-licenses.ps1. Selected version supports the project Go 1.25 baseline. |
| Go modules | Dependencies actually compiled into the distributed Go binary, not the entire module graph | Actual selected versions and source URLs recorded in licenses/sourceURLs.tsv. Root-level LICENSE*/COPYING* files copied from each selected module directory; license type is not guessed. |

The original project assets app-icon.svg, app-icon.png and app-icon.ico were authored via the OpenAI API; they are not borrowed vendor logos. This asset creation does not introduce any runtime OpenAI API dependency.

Portable bundles include docs/opskat-comparison.md alongside this notice.

License collection runs only into fresh build staging. Missing module license files fail the build and require review; a source URL alone is not a license grant. Before distributing a full bundle, verify that the runtime's own licensing material is present and applicable. No private credentials, target fingerprints, or operational data belong in notices.
