## [6.4.0](https://github.com/AtomiCloud/nix-registry/compare/v6.3.0...v6.4.0) (2026-11-18)


### ⬆️ Packages Updated ⬆️

* **obs:** aggregations reset the live label scope ([b364e90](https://github.com/AtomiCloud/nix-registry/commit/b364e902b89ff67b55f20bb808738785073b8c3a))
* **obs:** binary ops keep one side's query-made labels ([8f79a84](https://github.com/AtomiCloud/nix-registry/commit/8f79a84363b2974762ac6bc0afda68b942273c44))
* **obs:** copy Primordial's vminsert relabel config exactly ([cc531e0](https://github.com/AtomiCloud/nix-registry/commit/cc531e0891e1975a5c85e0be8ff2bcc8b4645100))
* **obs:** correct the README's live label and Tempo cutoff wording ([323ad3b](https://github.com/AtomiCloud/nix-registry/commit/323ad3b9c52a4d6fdf00a4d48eb289a682ea3ca8))
* **obs:** follow the engine's result-label rules in live ([de0a0ec](https://github.com/AtomiCloud/nix-registry/commit/de0a0ec8635a0bf4f40a8dcc5207713acffb7d97))
* **obs:** judge label spelling in pod keys, variables, runs ([d0d14e0](https://github.com/AtomiCloud/nix-registry/commit/d0d14e06777370a05a66467ef4c99c07e77136b3))
* **obs:** keep dots in VictoriaMetrics label names ([4783f33](https://github.com/AtomiCloud/nix-registry/commit/4783f337286a1e9bc18727f10a42dc67b9dd1a4f))
* **obs:** mirror the labelmap in the sandbox, exact TraceQL names ([41736c5](https://github.com/AtomiCloud/nix-registry/commit/41736c50a375c89b1cab848648dae3aff8befb9a))
* **obs:** no real landscape names in the new fixtures and README ([79ee4e9](https://github.com/AtomiCloud/nix-registry/commit/79ee4e96066bba971a201a7afd934b580be64f93))
* **obs:** obs 0.3.0, note trace panels run under Tempo limits ([679c829](https://github.com/AtomiCloud/nix-registry/commit/679c829cacda42258eb54a877b78eca7c9d92b2e))
* **obs:** obs.yaml round 2, limits and image checks ([d456604](https://github.com/AtomiCloud/nix-registry/commit/d456604b70f1d217b54743399f22fc3ee942676b))
* **obs:** obs.yaml versions and thresholds, evaluator check ([3480d71](https://github.com/AtomiCloud/nix-registry/commit/3480d71917efdbd8a906546bbd1c48ddc4dc041d))
* **obs:** query-made labels and label_values parsing ([30592d3](https://github.com/AtomiCloud/nix-registry/commit/30592d34a892f82dead18798e73afe954fe19031))
* **obs:** report a wrong label spelling once in live ([d5d06aa](https://github.com/AtomiCloud/nix-registry/commit/d5d06aa33f0846ac03ea7627410847abca52a23b))
* **obs:** scalar sides and group_left/right follow the engine ([ce7b043](https://github.com/AtomiCloud/nix-registry/commit/ce7b0431ac1ed560f6afa23318fb7d54629b11ab))

## [6.3.0](https://github.com/AtomiCloud/nix-registry/compare/v6.2.0...v6.3.0) (2026-11-18)


### 🐛 Bug Fixes 🐛

* **obs:** keep release gate fixtures independent of identity ([5024ffa](https://github.com/AtomiCloud/nix-registry/commit/5024ffafc4b58f95a168fb34f572272149a34153))
* **obs:** parity comparator rejects non-finite mismatches ([92d7ac3](https://github.com/AtomiCloud/nix-registry/commit/92d7ac3d20ac48b21fb89d30ef9df57f6f44ffb5))
* **obs:** preserve TraceQL values and query models ([e462cb4](https://github.com/AtomiCloud/nix-registry/commit/e462cb4af6269da41d2000b0c8931aa32f229c5a))
* **obs:** remove parity anonymous volumes during cleanup ([24867be](https://github.com/AtomiCloud/nix-registry/commit/24867be183e9940a19609f238f6d27fc4b388c07))
* **obs:** sandbox test stubs use /bin/sh ([e5246de](https://github.com/AtomiCloud/nix-registry/commit/e5246de2d09f1fc42a703fdc1f67ba94e1572ce8))


### ⬆️ Packages Updated ⬆️

* **obs:** a bare dollar is not a dashboard variable ([4e5c736](https://github.com/AtomiCloud/nix-registry/commit/4e5c73618def87fb0b3a8a17d4434a1044d30024))
* **obs:** a competing registration poisons the signal port ([01689d1](https://github.com/AtomiCloud/nix-registry/commit/01689d1d6a51dc22e07d8d4c0c5d2ac123290136))
* **obs:** activate trace parity after dependencies land ([387e7de](https://github.com/AtomiCloud/nix-registry/commit/387e7de236b1aa09ed6130dbcdaffb245d4a72b9))
* **obs:** address store, symbol and sizing review ([0e6ca43](https://github.com/AtomiCloud/nix-registry/commit/0e6ca43b4de0eb477975cf0dad94bceae56d9dd9))
* **obs:** alerts in the embedded engines ([e9d06dd](https://github.com/AtomiCloud/nix-registry/commit/e9d06dd20efc8492ecf132f32e0ccd889d63770a))
* **obs:** align panel query times with Grafana ([8d8f1e6](https://github.com/AtomiCloud/nix-registry/commit/8d8f1e66ba67d0a01415d64cb420e8686f15816e))
* **obs:** allow-list store move routing and single restore ([ff6545f](https://github.com/AtomiCloud/nix-registry/commit/ff6545fea4729f31363fc2296996972f88e2de8d))
* **obs:** apply chart query limits and gate releases ([95c4444](https://github.com/AtomiCloud/nix-registry/commit/95c4444f6fe5a7a37a651f20bf7a7f5f989f1771))
* **obs:** assert pruning checks capture query inputs ([1442851](https://github.com/AtomiCloud/nix-registry/commit/144285155f0d14b30a3fb1ae868a519b140a05a0))
* **obs:** bun generator for obs gen (signals.ts, golden, tsc check) ([231088d](https://github.com/AtomiCloud/nix-registry/commit/231088d549801376554ff51503cc604fbd06419c))
* **obs:** bun golden tree, check and gen e2e tests, README ([0ad0b26](https://github.com/AtomiCloud/nix-registry/commit/0ad0b26441c1f9238a0ec21eed869ff963ca14d7))
* **obs:** Bun guard reads a frozen deep copy of its tables ([fc51359](https://github.com/AtomiCloud/nix-registry/commit/fc51359f001d6baa59070e94f8e6a24d898ac6bc))
* **obs:** Bun registers its tables once and poisons on a foreign one ([5e01365](https://github.com/AtomiCloud/nix-registry/commit/5e01365d99dbbd1dd254245dd7f084c492ff2b4f))
* **obs:** centralize pins and compare real store results ([1c41555](https://github.com/AtomiCloud/nix-registry/commit/1c41555942a0f8f325e04d0735735ff620c6a4cc))
* **obs:** chart check nested image of an own path is own ([2f51e4a](https://github.com/AtomiCloud/nix-registry/commit/2f51e4a4c4b1e67770ff990af3a6497523dc030e))
* **obs:** chart check proof per alias and per image ([bc12041](https://github.com/AtomiCloud/nix-registry/commit/bc120411395aa84ac2418108830b0df2d4b19cb6))
* **obs:** chart check tempo wrapper with nested image ([aff234e](https://github.com/AtomiCloud/nix-registry/commit/aff234ec3585e99288ecae4c6c88879517a8c022))
* **obs:** check hot rows apart from identity context ([9621bd7](https://github.com/AtomiCloud/nix-registry/commit/9621bd7f0953aaf4587ede718436a703794e654f))
* **obs:** check store move manifests against live objects ([4175832](https://github.com/AtomiCloud/nix-registry/commit/4175832a13b9098d3673eed34568073c059e34cd))
* **obs:** close signal forging with a run-time guard per language ([6da9a8e](https://github.com/AtomiCloud/nix-registry/commit/6da9a8ed5259fa89392a02faa243aaa99d867582))
* **obs:** close store move round-6 review findings ([5a8f3b0](https://github.com/AtomiCloud/nix-registry/commit/5a8f3b02ac43a0e3cd4169e619ab152a00be22e9))
* **obs:** compare duplicate functions in source order ([f4b5d72](https://github.com/AtomiCloud/nix-registry/commit/f4b5d7245bfb9abd4917b3d6905d7d5dbb8aeaff))
* **obs:** compare Grafana panels and preserve parity evidence ([ef34604](https://github.com/AtomiCloud/nix-registry/commit/ef3460484cee4703ed86ecf57f32a78ca120c158))
* **obs:** compare raw alert counts and isolate profiles ([4d07431](https://github.com/AtomiCloud/nix-registry/commit/4d074317a20950b9cc790b09aac02b2d02eaf9bd))
* **obs:** contract schema, generator interface and fixture contract ([e7fb941](https://github.com/AtomiCloud/nix-registry/commit/e7fb9415f9261eb948c696b66a1b69bc1fb4965b))
* **obs:** copy writable interface stubs for checks ([68278f1](https://github.com/AtomiCloud/nix-registry/commit/68278f17bb3c2294f8c8dfc156dcbd28c7d31284))
* **obs:** count significant duplicate declarations on both sides ([be61aad](https://github.com/AtomiCloud/nix-registry/commit/be61aadfe037b212fb37336b93536c389c993562))
* **obs:** digest-pinned images, engine tag shapes, usage ([ec9b0d1](https://github.com/AtomiCloud/nix-registry/commit/ec9b0d1dfdf24cc304e251a19f4cbdd945804c6a))
* **obs:** dotnet and dart generators for obs gen ([28539dc](https://github.com/AtomiCloud/nix-registry/commit/28539dcba26f1d315ed28e7d66143a154c69e94e))
* **obs:** dotnet and dart targets back in the fixture contract ([7a9b36d](https://github.com/AtomiCloud/nix-registry/commit/7a9b36da29f1f48a53a2f14adaff583a82d5dd03))
* **obs:** drop duplicate contract span-name wiring ([4daff7e](https://github.com/AtomiCloud/nix-registry/commit/4daff7e2dbbd812ba0be53c2574d49f7d44e46a3))
* **obs:** exclude fixture generator from the binary package ([ffe0f27](https://github.com/AtomiCloud/nix-registry/commit/ffe0f27bb55788e3ab39a755f1ce774b87fddb29))
* **obs:** final integration pass ([63a3673](https://github.com/AtomiCloud/nix-registry/commit/63a3673c0017159998486da57ef0191f20a5010a))
* **obs:** fix PR 111 review 3: flush, units, event forms, trace scope ([6a28b50](https://github.com/AtomiCloud/nix-registry/commit/6a28b50c1cd3adaca9e95a2c796084ca4345789d))
* **obs:** fix PR 111 review: stream fields, zero values, name checks ([a891179](https://github.com/AtomiCloud/nix-registry/commit/a8911793eac4dcfd57506cadf022671d2e86d82b))
* **obs:** fix PR 111 round 2 findings and add generator fuzz ([c049787](https://github.com/AtomiCloud/nix-registry/commit/c0497879bb47dd95fd385c7cf28f47c636eb69df))
* **obs:** fix sandbox volumes, settings and look ([0fd28be](https://github.com/AtomiCloud/nix-registry/commit/0fd28beb9a9d182015be66cfc93e5800ab24a16b))
* **obs:** fix store move DaemonSet, routing and rollback ([075c421](https://github.com/AtomiCloud/nix-registry/commit/075c421ac866bce6edef8e4450eecebe828f6403))
* **obs:** fix TraceQL dispatch and link regressions ([bdc2cc0](https://github.com/AtomiCloud/nix-registry/commit/bdc2cc02825e241af35a68345c89e2210d137ad8))
* **obs:** fixture targets go and bun, bun output prettier-stable ([f84d972](https://github.com/AtomiCloud/nix-registry/commit/f84d972ce3f48f219e7da264ccc36dde1252e7fa))
* **obs:** format pinned referee setup ([87c1143](https://github.com/AtomiCloud/nix-registry/commit/87c1143da398fed08d477c5cb1eb2b3f81dfbdcd))
* **obs:** go generator, gen driver and golden fixture ([7d5ada3](https://github.com/AtomiCloud/nix-registry/commit/7d5ada392b8bb7a3dcc94bb6b42d62f0e6019286))
* **obs:** guard checks label types, values and one-shot tables ([b1b952f](https://github.com/AtomiCloud/nix-registry/commit/b1b952f030accb5534d40b110a1b1b3a746060e3))
* **obs:** guard exact pairing against shifted duplicate groups ([adabd1a](https://github.com/AtomiCloud/nix-registry/commit/adabd1af90fc676aa3ec4dcf26c8121ccc94c74a))
* **obs:** harden profile identity and series assertions ([83286d9](https://github.com/AtomiCloud/nix-registry/commit/83286d9a2bd8c2ab9774fc9914c3f1119e812f2d))
* **obs:** integrate alert parity and limit refusals ([26cf35f](https://github.com/AtomiCloud/nix-registry/commit/26cf35f9dcc35a320440379c64e1b87b31135d45))
* **obs:** integrate TraceQL parity with base ([0ac372e](https://github.com/AtomiCloud/nix-registry/commit/0ac372e7e8d313907ed7c9ef8a89e2b0dd898925))
* **obs:** keep parity current with sandbox base ([16b4ebb](https://github.com/AtomiCloud/nix-registry/commit/16b4ebb52a1f0a8ee96068119d0a35f13b0a1cd9))
* **obs:** keep symbolizer an optional runtime tool ([6f7b11f](https://github.com/AtomiCloud/nix-registry/commit/6f7b11f207d7639f2a1e7aed143f78a83269312e))
* **obs:** live drops assumed filters and names recording-rule reads ([74bfeeb](https://github.com/AtomiCloud/nix-registry/commit/74bfeeb11fe8b39d82a52759b42dd25796f788d8))
* **obs:** load only read log events, stop pinning VL buffers ([485f932](https://github.com/AtomiCloud/nix-registry/commit/485f9321900c15ac0d8332e831a144cf172c43ae))
* **obs:** merge obs-full into final pass ([a8b55b8](https://github.com/AtomiCloud/nix-registry/commit/a8b55b8825151848b3f8aa49995b87d25525a711))
* **obs:** merge obs-full into parity ([a607a5b](https://github.com/AtomiCloud/nix-registry/commit/a607a5b457e93a3bd16208d897978aff932b3367))
* **obs:** obs alerts evaluates TraceQL rules ([98bbd9a](https://github.com/AtomiCloud/nix-registry/commit/98bbd9addf970e57ecf0de077e5000d02cf1eda7))
* **obs:** obs alerts GC settings for per-query engine buffers ([dd04737](https://github.com/AtomiCloud/nix-registry/commit/dd04737361c17d18c5dac1ef8609fdb028bf8b8f))
* **obs:** obs check, obs gen and contract name checks ([b3acd17](https://github.com/AtomiCloud/nix-registry/commit/b3acd179bfe238b68ef18bfc77e1291ec6689fd2))
* **obs:** OBS_VERIFY_PRUNE self-check for obs alerts ([a3c86b4](https://github.com/AtomiCloud/nix-registry/commit/a3c86b4e47c66b40a9fe7edee8170f83cbdd0c28))
* **obs:** own engine image proves a dependency ([d21ff90](https://github.com/AtomiCloud/nix-registry/commit/d21ff903a1a52697c72866e2e1dc45f1afa5d891))
* **obs:** pin declaration identity in median regression tests ([3988138](https://github.com/AtomiCloud/nix-registry/commit/398813872d759b687ff81a843c4a0ba1324cdadc))
* **obs:** pin referee digests and add feature parity adapters ([e006a1c](https://github.com/AtomiCloud/nix-registry/commit/e006a1cdc7d256daccd9a9a95288465544e8f4f9))
* **obs:** preserve function identity and check every series ([f8ce77f](https://github.com/AtomiCloud/nix-registry/commit/f8ce77f238f980704d13ba29c9c61c3a15955369))
* **obs:** preserve LogsQL stats identities and run isolation ([aa2085a](https://github.com/AtomiCloud/nix-registry/commit/aa2085af95fbe76612a9751bcc46f04d15f74a94))
* **obs:** preserve version output for vendored engine pins ([f730156](https://github.com/AtomiCloud/nix-registry/commit/f7301566b364665695a0abeb2ae21253f1c78290))
* **obs:** profile summarize, baseline and compare ([ba313d0](https://github.com/AtomiCloud/nix-registry/commit/ba313d0826de905d1259e9025649701c1394d4fd))
* **obs:** project trace sources with lazy metric loading ([56e3e4a](https://github.com/AtomiCloud/nix-registry/commit/56e3e4a5776173bba93bdb72563f6536a99672f5))
* **obs:** prune log events only from a sound superset ([6dc3120](https://github.com/AtomiCloud/nix-registry/commit/6dc3120b7f938bcc53408b7eed8ec45386fd8dee))
* **obs:** README section for the VictoriaLogs speed table ([bc7bde7](https://github.com/AtomiCloud/nix-registry/commit/bc7bde724bd74fe8a3fa99dbd9b7cb0d5640e815))
* **obs:** refuse missing fixture and alert minute coverage ([5044bd5](https://github.com/AtomiCloud/nix-registry/commit/5044bd5c99a6a8e23bbf763e24ac5463ad7dcefc))
* **obs:** refuse readiness and service mutation bypasses ([990c94e](https://github.com/AtomiCloud/nix-registry/commit/990c94ebc3c4813f1e357ba42304e4d4457c93e1))
* **obs:** release fixes from the final review ([fb59a93](https://github.com/AtomiCloud/nix-registry/commit/fb59a934dbde61e1959747a190794d422ccbbeeb))
* **obs:** require complete pruning verification snapshots ([fc500a0](https://github.com/AtomiCloud/nix-registry/commit/fc500a0058e0b73034866655625d5561a3a86107))
* **obs:** require live query snapshots in pruning checks ([0c523ec](https://github.com/AtomiCloud/nix-registry/commit/0c523ec41aaabd5cf5424fe097662213eed417cd))
* **obs:** reserve exact declarations before source ordering ([2a158fb](https://github.com/AtomiCloud/nix-registry/commit/2a158fb2a79eb8147d325e16affc1e6631cef181))
* **obs:** resolve alert parity review findings ([d7b6d97](https://github.com/AtomiCloud/nix-registry/commit/d7b6d97abb1b314fcce0ef8bbaa29b6c36219096))
* **obs:** restore the profile noise formula, guard rm paths in run.sh ([ec87b28](https://github.com/AtomiCloud/nix-registry/commit/ec87b28169d5c3034eba3e1073c5f8bdae71c292))
* **obs:** retain profile and image build evidence ([46c5d91](https://github.com/AtomiCloud/nix-registry/commit/46c5d9104cd80d677222d222e7147195990a69bb))
* **obs:** sandbox, scrape and look ([9457545](https://github.com/AtomiCloud/nix-registry/commit/9457545484228259ee2e81804c0d0a5939957b0e))
* **obs:** selector terms need the label key, even for empty values ([ed1e20f](https://github.com/AtomiCloud/nix-registry/commit/ed1e20f952207fe415b558fc5e0055d9b7642172))
* **obs:** series assert against the contract ([0257d0b](https://github.com/AtomiCloud/nix-registry/commit/0257d0b2368f4b4dbbdb9639afc53104e9e0d3bb))
* **obs:** share interface types in generated signals ([cb2940c](https://github.com/AtomiCloud/nix-registry/commit/cb2940cc9b178e6ad40d3dd438746c5cbb74a2b5))
* **obs:** speed fixtures carry the contract and measured expectations ([e9c5560](https://github.com/AtomiCloud/nix-registry/commit/e9c55605e41bb233ed062c37c80432ef072d795a))
* **obs:** speed fixtures name alerts by uid, scale count thresholds ([8715a88](https://github.com/AtomiCloud/nix-registry/commit/8715a88125a9d6c7e2a66135915515cbfbc0a659))
* **obs:** speed table after the alerts profiling fixes ([e2713cb](https://github.com/AtomiCloud/nix-registry/commit/e2713cb49395c7e586ac93eba3062fb2052c55ff))
* **obs:** speed-table fixture generator and benchmark runner ([3a63c4e](https://github.com/AtomiCloud/nix-registry/commit/3a63c4e2b2d7fc2c9b7e59998bc59a779242abc1))
* **obs:** split event form fixture into phrase and prefix rules ([14c495b](https://github.com/AtomiCloud/nix-registry/commit/14c495bb1b4a5d7137a85fe159088305d0c384da))
* **obs:** store move builds every change to existing objects ([be665f6](https://github.com/AtomiCloud/nix-registry/commit/be665f6539e360769db2350198d7e355a2036c33))
* **obs:** store move, stack decoder and sizing ([c245302](https://github.com/AtomiCloud/nix-registry/commit/c24530287815cf17b656f6d449c09b5d8a4459e0))
* **obs:** tempo chart image path in chart check ([13bc95a](https://github.com/AtomiCloud/nix-registry/commit/13bc95a4d5b2a94bc964a4402913c84d5a5e4b1c))
* **obs:** test escaped sibling matchers ([058a799](https://github.com/AtomiCloud/nix-registry/commit/058a799d53823f6d977264e3294e4c32bd7c2377))
* **obs:** trace alerts follow Tempo's end cutoff and alignment ([1fa04f3](https://github.com/AtomiCloud/nix-registry/commit/1fa04f3615788ea6576121de6955035a26f20ea4))
* **obs:** TraceQL checks and obs links ([477c452](https://github.com/AtomiCloud/nix-registry/commit/477c452d9e1332375e056731877aaea7bb0dc2ce))
* **obs:** TraceQL projection drift, trace ID variable ([d4eecbd](https://github.com/AtomiCloud/nix-registry/commit/d4eecbd3e7df1667b14f8847eb990447d8e15718))
* **obs:** typecheck expects CS1729 for a made-outside C# value ([95c55ae](https://github.com/AtomiCloud/nix-registry/commit/95c55aeff202fc872375e1c2381607b77532f0ce))
* **obs:** validate nested log queries and snapshot pruning inputs ([7f938fc](https://github.com/AtomiCloud/nix-registry/commit/7f938fc1e23e5b07edd7e6ca8a13db4f7b002d24))
* **obs:** vendorHash for the added field filter forms ([1d38276](https://github.com/AtomiCloud/nix-registry/commit/1d38276c59f045d8aa320cc717cb67ccffc15ab0))
* **obs:** vendorHash for the added field-filter walker ([39e3de3](https://github.com/AtomiCloud/nix-registry/commit/39e3de34140e49f28914d80a6be27679261b5e34))
* **obs:** vendorHash for the marked VictoriaLogs file change ([b17221d](https://github.com/AtomiCloud/nix-registry/commit/b17221d4f4ad99cd99f0ae95451992b7b7b026d3))
* **obs:** vendorHash for the pprof reader ([69d1e2e](https://github.com/AtomiCloud/nix-registry/commit/69d1e2e9c78a04204d23322c77150c1736a94f09))
* **obs:** vendorHash with the contract and pprof modules ([9ac06d2](https://github.com/AtomiCloud/nix-registry/commit/9ac06d212bc84a44ca48aefa43373acb0cbefb18))
* **obs:** verify alert evaluation and event filters ([6080899](https://github.com/AtomiCloud/nix-registry/commit/60808993ff977f29d290b8220b8c993ad29b63b9))
* **obs:** verify recursive log policy and pruning ([25ded56](https://github.com/AtomiCloud/nix-registry/commit/25ded560162e2c36076539db01f5e5eb508f0a62))
* **obs:** verify temporal query request bounds ([3d80e3f](https://github.com/AtomiCloud/nix-registry/commit/3d80e3fc2ae502d7d561665766d3b454506dd556))
* **obs:** verify TraceQL metrics and record real parity results ([26354a2](https://github.com/AtomiCloud/nix-registry/commit/26354a22406e66b27973db8b9c2af83fe2fd1781))
* **obs:** walk orphans from the git root, refuse escaping out paths ([3b35d68](https://github.com/AtomiCloud/nix-registry/commit/3b35d68fed61e85862ece70faaa3ace6f64f3cc1))


### Documentation Updates

* ul run.sh checks --from ranking inside the pastry folder ([72796a3](https://github.com/AtomiCloud/nix-registry/commit/72796a356f4f72a5155088fbcd667cf2af64e0e3))
* use a made-up cooking example in the ul test fixture ([b954485](https://github.com/AtomiCloud/nix-registry/commit/b954485c889f513873c5f94a466446eb67a33870))

## [6.2.0](https://github.com/AtomiCloud/nix-registry/compare/v6.1.0...v6.2.0) (2026-11-18)


### 🐛 Bug Fixes 🐛

* **ul:** remove the session layer ([bcf0b9e](https://github.com/AtomiCloud/nix-registry/commit/bcf0b9e2f808bd16462e0f558bdb9550779512e7))
* **ul:** run git in the C locale, resolve relative --from on real path ([8fa6e41](https://github.com/AtomiCloud/nix-registry/commit/8fa6e41ddc31ebc0308a114c1e68a96b215f7d4d))
* **ul:** settle the ten CodeRabbit findings on the ul PR ([22022f1](https://github.com/AtomiCloud/nix-registry/commit/22022f12f4789bf916f5b807b78ad7cb89951374))
* **ul:** stop on a refusing git, resolve symlinks, report bad reads ([3867f0d](https://github.com/AtomiCloud/nix-registry/commit/3867f0dbf9b92cf2221c3cae95619e7821e39827))
* **ul:** test --root through a symlinked cwd, skip untranslated git ([8581a64](https://github.com/AtomiCloud/nix-registry/commit/8581a6405bb22cfbed810bad51400b320e9e5f31))


### ✨ New Packages ✨

* **ul:** add the ubiquitous-language tool ([baee23c](https://github.com/AtomiCloud/nix-registry/commit/baee23c9b0fd7b5168840233e8c90538487813dc))

## [6.1.0](https://github.com/AtomiCloud/nix-registry/compare/v6.0.0...v6.1.0) (2026-11-16)


### 🐛 Bug Fixes 🐛

* **obs:** fill $__auto_interval_<var>, name an unset datasource variable ([9f0a227](https://github.com/AtomiCloud/nix-registry/commit/9f0a227ba340d499bd773c1a36c6567c74bf3d49))
* **obs:** report a panel query that does not parse once ([24c82cd](https://github.com/AtomiCloud/nix-registry/commit/24c82cda777604fd9229b500d1cf692aea9702ac))


### ✨ New Packages ✨

* **obs:** add the observability query check ([eda3d81](https://github.com/AtomiCloud/nix-registry/commit/eda3d812944ff055809a60cd4258a69558a67926))


### ⬆️ Packages Updated ⬆️

* **obs:** add the local parity run against real stores ([bb3f03e](https://github.com/AtomiCloud/nix-registry/commit/bb3f03ecb9b17d7371e63634e9db09763915d105))

## [6.0.0](https://github.com/AtomiCloud/nix-registry/compare/v5.15.0...v6.0.0) (2026-11-16)


### ⚠ BREAKING CHANGES

* `chart-checks labels` is gone, and a chart-checks.yaml
that sets `chart.labels` or `chart.labelPrefix` is now refused as an
unknown key (exit 3).

### ✨ New Packages ✨

* kubectl-validate from a pinned main commit (Kubernetes 1.35) ([8443b6d](https://github.com/AtomiCloud/nix-registry/commit/8443b6d62d455282f800020d598d6ded2238184d))


### ⬆️ Packages Updated ⬆️

* chart-checks check-chart reports every failure ([ab45427](https://github.com/AtomiCloud/nix-registry/commit/ab454275f6c1acf0de959c098d3b076b3b542316))
* infralint and infralint-helm carry chart-checks ([7d9b995](https://github.com/AtomiCloud/nix-registry/commit/7d9b9958569f2d6f3881f3e86487892ac4dd3b1a))


### ❌ Removed Packages ❌

* chart-checks drops its own every-kind labels step ([c1b90f3](https://github.com/AtomiCloud/nix-registry/commit/c1b90f304b07dc9972f1087394ddba3cb4b1cca9))

## [5.15.0](https://github.com/AtomiCloud/nix-registry/compare/v5.14.0...v5.15.0) (2026-11-15)


### 🐛 Bug Fixes 🐛

* fail closed when the label check helper fails ([0e6b07d](https://github.com/AtomiCloud/nix-registry/commit/0e6b07dddfe37ae19351f7cc60e257fa1033fa5c))
* isolate each check workspace and guard render output ([f9f52b6](https://github.com/AtomiCloud/nix-registry/commit/f9f52b669a3b55bdf07933b21476a5167b49515e))
* keep render output inside the repository ([b88dadd](https://github.com/AtomiCloud/nix-registry/commit/b88daddcf4c99a88e57aa8759225aa88b92947f2))


### ⬆️ Packages Updated ⬆️

* chart-checks gains chart commands and per-policy values ([4518c46](https://github.com/AtomiCloud/nix-registry/commit/4518c4681e61fd88883c63ef04e2956c9bc23cda))

## [5.14.0](https://github.com/AtomiCloud/nix-registry/compare/v5.13.0...v5.14.0) (2026-11-14)


### 🐛 Bug Fixes 🐛

* **problemgen:** address CodeRabbit review threads ([53c750c](https://github.com/AtomiCloud/nix-registry/commit/53c750c663f78497bd6ec07dd40f1744347a2eaa))
* **problemgen:** close review gaps in lint, record, diff and packaging ([7e9962f](https://github.com/AtomiCloud/nix-registry/commit/7e9962fcc9e12ccfca6ea85533cc5c9c748a133b))
* **problemgen:** close second review gaps; add generative compile test ([48bc363](https://github.com/AtomiCloud/nix-registry/commit/48bc3634c4d7271ff91a2e0c23f93f06a284cdd3))
* **problemgen:** contain manifest deletions; third review fixes ([b3eec4e](https://github.com/AtomiCloud/nix-registry/commit/b3eec4ed163a1541120992fe02ebb948a5c4ccf6)), closes [#64](https://github.com/AtomiCloud/nix-registry/issues/64)
* **problemgen:** guard every write path; one origin rule everywhere ([cf39df7](https://github.com/AtomiCloud/nix-registry/commit/cf39df722a33fb86c1726c4a2cdfccee1495d088))


### ✨ New Packages ✨

* **problemgen:** add definition-first RFC 9457 problem generator ([30fb535](https://github.com/AtomiCloud/nix-registry/commit/30fb5358890b598edae7b98ddbe662cdac9a9b73))


### ⬆️ Packages Updated ⬆️

* **problemgen:** portal at run time; six more generics ([402d065](https://github.com/AtomiCloud/nix-registry/commit/402d06504b276d9d441b700d77cd3a996b499cea))

## [5.13.0](https://github.com/AtomiCloud/nix-registry/compare/v5.12.0...v5.13.0) (2026-11-09)


### 🐛 Bug Fixes 🐛

* **drv:** carry root required spec into wrapped CRD ([c1b7aef](https://github.com/AtomiCloud/nix-registry/commit/c1b7aefff8ef91a6cef93c91879487ccccbb1133))


### ✨ New Packages ✨

* **chart-checks:** add offline manifest validation tool ([29613b4](https://github.com/AtomiCloud/nix-registry/commit/29613b4453ba760db956d7ebebc9a122dbc3785f))

## [5.12.0](https://github.com/AtomiCloud/nix-registry/compare/v5.11.0...v5.12.0) (2026-11-06)


### 🐛 Bug Fixes 🐛

* **drv:** trim gorelease comments ([2c6b3d6](https://github.com/AtomiCloud/nix-registry/commit/2c6b3d6cd1aae311915ad30caa1a43ee362cf16f))


### ✨ New Packages ✨

* **gorelease:** add golang.org/x/exp/cmd/gorelease ([fc32ede](https://github.com/AtomiCloud/nix-registry/commit/fc32edea386390eb8bccc889252e657c8df0dafa))

## [5.11.0](https://github.com/AtomiCloud/nix-registry/compare/v5.10.0...v5.11.0) (2026-11-03)


### ⬆️ Packages Updated ⬆️

* **patch:** bump go to 1.26.10 ([4135e27](https://github.com/AtomiCloud/nix-registry/commit/4135e27470551cf4d968dd01bc6fc7a169e624df))

## [5.10.0](https://github.com/AtomiCloud/nix-registry/compare/v5.9.1...v5.10.0) (2026-11-02)


### ✨ New Packages ✨

* **buildBunCli:** add reusable bun-compiled CLI builder ([302e49a](https://github.com/AtomiCloud/nix-registry/commit/302e49a0d7cbcd3591233af3662b75aa4cefe6ec))


### Documentation Updates

* point BunWrapper test command at the fixture package ([1e0c3ea](https://github.com/AtomiCloud/nix-registry/commit/1e0c3ea1622827c1d0fcdfcfd24474e44d0f0d8f))

## [5.9.1](https://github.com/AtomiCloud/nix-registry/compare/v5.9.0...v5.9.1) (2026-10-31)


### 🐛 Bug Fixes 🐛

* symlink bundle tools instead of copying them ([bd2a19e](https://github.com/AtomiCloud/nix-registry/commit/bd2a19e8fd4295f7d7a9d61391f14ffae9f1d39e)), closes [#98](https://github.com/AtomiCloud/nix-registry/issues/98)

## [5.9.0](https://github.com/AtomiCloud/nix-registry/compare/v5.8.0...v5.9.0) (2026-10-30)


### ⬆️ Packages Updated ⬆️

* **minor:** bump cyanprint to 4.10.1 ([1e45a06](https://github.com/AtomiCloud/nix-registry/commit/1e45a0622b502d6efbcaf16304b1908ff135feeb))

## [5.8.0](https://github.com/AtomiCloud/nix-registry/compare/v5.7.1...v5.8.0) (2026-10-30)


### 🐛 Bug Fixes 🐛

* **drv:** skip dlint release-gating only when its key is absent ([8ee42fc](https://github.com/AtomiCloud/nix-registry/commit/8ee42fcdd477dcffe05afa62bc7a2b588fe1513f))


### ✨ New Packages ✨

* **dlint:** add the release-gating check ([7dabce4](https://github.com/AtomiCloud/nix-registry/commit/7dabce4702cab27490e5fa971f28e40d713c4ca4))


### ⬆️ Packages Updated ⬆️

* **patch:** make dlint release-gating opt-in under --all-configured ([13a4485](https://github.com/AtomiCloud/nix-registry/commit/13a44857e72cdd0da325e4f180ddd5a6b4f5c2c9))

## [5.7.1](https://github.com/AtomiCloud/nix-registry/compare/v5.7.0...v5.7.1) (2026-10-30)


### 🐛 Bug Fixes 🐛

* **drv:** fetch bun script deps in a fixed-output derivation ([875d4df](https://github.com/AtomiCloud/nix-registry/commit/875d4df08aa202d55af182a9d11eff301418c9a6))
* **drv:** include bunfig.toml in the bun deps digest ([b9fa245](https://github.com/AtomiCloud/nix-registry/commit/b9fa24505bee260de52e423fafda8f380476a591))
* **drv:** re-run the bun deps install whenever its lockfile changes ([5103826](https://github.com/AtomiCloud/nix-registry/commit/5103826935da2266e2a9cbc0f4e58095de1985a6))

## [5.7.0](https://github.com/AtomiCloud/nix-registry/compare/v5.6.0...v5.7.0) (2026-08-09)


### 🐛 Bug Fixes 🐛

* **drv:** pin dotnetPackage to the fleet's SDK patch and assert it ([ddd9749](https://github.com/AtomiCloud/nix-registry/commit/ddd974995d29226230d5e6016f3eded0053c1819))


### ⬆️ Packages Updated ⬆️

* **minor:** export dotnet ecosystem tools and validator runtime ([febf715](https://github.com/AtomiCloud/nix-registry/commit/febf7153e8ccce89a7ba6bff9d576622102bf531))

## [5.6.0](https://github.com/AtomiCloud/nix-registry/compare/v5.5.0...v5.6.0) (2026-08-09)


### ⬆️ Packages Updated ⬆️

* **patch:** make dlint no-custom-derivations comment-aware ([1088bb7](https://github.com/AtomiCloud/nix-registry/commit/1088bb705ffc50665f001b72b3552b9114afad86))

## [5.5.0](https://github.com/AtomiCloud/nix-registry/compare/v5.4.0...v5.5.0) (2026-08-09)


### 🐛 Bug Fixes 🐛

* state a unit for every count in resolver-smoke's summary line ([71850ff](https://github.com/AtomiCloud/nix-registry/commit/71850ff9a55562bbbaa69455b71263ea592b8172))


### ✨ New Packages ✨

* **helm-schema:** hoist the helm schema wrapper out of helm-wrapper ([79e0d0a](https://github.com/AtomiCloud/nix-registry/commit/79e0d0aac5b582f27b6e41f425e9c6c3116c40ca))


### ⬆️ Packages Updated ⬆️

* **patch:** give resolver-smoke a complete lost-binding list mode ([0c64c81](https://github.com/AtomiCloud/nix-registry/commit/0c64c81ffb1f013339b37a81af3c7b6a5a15fba4))
* **minor:** hoist dotnet-base's cyanprint internals into the registry ([6534490](https://github.com/AtomiCloud/nix-registry/commit/65344903288483b1d04c2dc43179ae5fa2afa0b5))

## [5.4.0](https://github.com/AtomiCloud/nix-registry/compare/v5.3.0...v5.4.0) (2026-08-09)


### ⬆️ Packages Updated ⬆️

* **minor:** split infrautils/infralint into axis-pure tool bundles ([8065961](https://github.com/AtomiCloud/nix-registry/commit/80659613c68acbca0e3c0e0f94cee829f91fe816))

## [5.3.0](https://github.com/AtomiCloud/nix-registry/compare/v5.2.0...v5.3.0) (2026-08-09)


### ⬆️ Packages Updated ⬆️

* **minor:** resolver-smoke vendors atomi/nix@3 ([dc1277c](https://github.com/AtomiCloud/nix-registry/commit/dc1277c77dc24870b8adf3d1f777d726460d3d79))
* **patch:** rework resolver smoke arms for atomi/nix@3 ([eb48f36](https://github.com/AtomiCloud/nix-registry/commit/eb48f36094407c91d8e7a33881e352bd05378b88))

## [5.2.0](https://github.com/AtomiCloud/nix-registry/compare/v5.1.0...v5.2.0) (2026-08-09)


### ⬆️ Packages Updated ⬆️

* **minor:** add published resolver smoke gate ([cd09c22](https://github.com/AtomiCloud/nix-registry/commit/cd09c229165d25f517ec2516ee9bc387513554d4))
* **minor:** harden resolver smoke maintenance ([4d90535](https://github.com/AtomiCloud/nix-registry/commit/4d9053577a011513d2b478b948ef0c2904148551))

## [5.1.0](https://github.com/AtomiCloud/nix-registry/compare/v5.0.0...v5.1.0) (2026-08-07)


### ⬆️ Packages Updated ⬆️

* **minor:** toolchain-smoke runs every binary, not just resolves it ([7a4c14d](https://github.com/AtomiCloud/nix-registry/commit/7a4c14de6c90dab11a1d2e043fb99fc1cd356de1))

## [5.0.0](https://github.com/AtomiCloud/nix-registry/compare/v4.5.0...v5.0.0) (2026-08-07)


### ⚠ BREAKING CHANGES

* **major:** the action-pins config key 'trustMap' (a JSON file
classifying every action) is retired. The check now reads ONE regex,
'trustedPattern': an action matching it is trusted (major-tag pin),
everything else is non-trusted (exact SHA + tag comment). There is no
non-trusted list to maintain — not matching IS the classification, so
an action nobody thought about gets the strictest pin by default.
Harness: 142/142.

Also drops the two vae blocks (verb/application/example doc helpers)
from atomi_release.yaml, per the same owner review round.

### ⬆️ Packages Updated ⬆️

* **major:** action-pins trusts by regex, retiring the trust map ([380ebb4](https://github.com/AtomiCloud/nix-registry/commit/380ebb43ac296c8488a4713ceb7899d796fbeecf))

## [4.5.0](https://github.com/AtomiCloud/nix-registry/compare/v4.4.1...v4.5.0) (2026-08-07)


### 🐛 Bug Fixes 🐛

* **dlint:** cover lock refusal paths ([633993e](https://github.com/AtomiCloud/nix-registry/commit/633993e2854816d001fdabfbc7d6f38c87d699fa))
* **dlint:** harden lint configuration ([79d374a](https://github.com/AtomiCloud/nix-registry/commit/79d374a29d98139ebaee24ac1c90843cfdf20cfe))


### ⬆️ Packages Updated ⬆️

* **minor:** add dlint lint and nixpkgs pins ([154cfca](https://github.com/AtomiCloud/nix-registry/commit/154cfca651164e5b61a72d27cfff7ed52cde5495))
* **dlint:** flake-text assertion joins the nixpkgs-pin check ([0e0a8be](https://github.com/AtomiCloud/nix-registry/commit/0e0a8beb4779201f064a931ece2562f82978d2e8))
* **dlint:** lint alias and nixpkgs-pin check in the real tool ([0918269](https://github.com/AtomiCloud/nix-registry/commit/0918269f3a368b815f8c8bf30ec973f4d018aa2a))
* **skills-sync:** multi-runtime — runtimes list with unioned resolution ([48a4256](https://github.com/AtomiCloud/nix-registry/commit/48a4256f6f0434b9a99400d406b61e8fd75383c1))
* **minor:** replace tiers with frozen enforcer ([c04b397](https://github.com/AtomiCloud/nix-registry/commit/c04b397e0c231733067d1943b0e953ddab979ee3))
* **dlint:** retire nixpkgs-pin (owner order) ([3546851](https://github.com/AtomiCloud/nix-registry/commit/3546851812b6155d2989ace82bcca9407e4df6ba))
* **dlint:** retire workflow-policy entirely ([2648267](https://github.com/AtomiCloud/nix-registry/commit/264826782eedfb7c832e509fb21ed4e8711697c8))

## [4.4.1](https://github.com/AtomiCloud/nix-registry/compare/v4.4.0...v4.4.1) (2026-08-06)


### Documentation Updates

* **pkg:** release the corrected skills-sync wiring documentation ([6c6d340](https://github.com/AtomiCloud/nix-registry/commit/6c6d34027474532e516f9057e78a577f41c9b5ff)), closes [#77](https://github.com/AtomiCloud/nix-registry/issues/77) [#77](https://github.com/AtomiCloud/nix-registry/issues/77)
* **skills-sync:** the shipped docs still describe the revoked never-in-hooks rule ([a0421c2](https://github.com/AtomiCloud/nix-registry/commit/a0421c2a569db6f79b71c09ba54c74f7db946591))

## [4.4.0](https://github.com/AtomiCloud/nix-registry/compare/v4.3.0...v4.4.0) (2026-08-06)


### 🐛 Bug Fixes 🐛

* **skills-sync:** evaluate D1 before the git precondition can pre-empt it ([3b1490d](https://github.com/AtomiCloud/nix-registry/commit/3b1490d8e6aeeb181269eba0f867b0eb8ce8505f))
* **skills-sync:** refuse when a repository declares diene packages but names no runtime ([e2f7ea6](https://github.com/AtomiCloud/nix-registry/commit/e2f7ea68aedfefb7371495b5ed21a9e949a1e234))


### ✨ New Packages ✨

* **skills-sync:** package the vendored-skill writer and freshness check ([0cc63c5](https://github.com/AtomiCloud/nix-registry/commit/0cc63c577aee8b32baba2ca640729a4e2a0089b5))
* **skills-sync:** the writer runs at pre-commit and CI, and refuses on the INDEX ([17089d0](https://github.com/AtomiCloud/nix-registry/commit/17089d0da07fba5a1f06c202ab73fadef80bedd8))


### Documentation Updates

* **skills-sync:** write the prune reasons, and control the selection from outside ([eaac7d8](https://github.com/AtomiCloud/nix-registry/commit/eaac7d8d53a1db85ccfdc2d9193f2dccb16d8794))

## [4.3.0](https://github.com/AtomiCloud/nix-registry/compare/v4.2.0...v4.3.0) (2026-08-06)


### ⬆️ Packages Updated ⬆️

* **minor:** bump the releaser pin to v2.0.0 ([5e59bd9](https://github.com/AtomiCloud/nix-registry/commit/5e59bd93ef1e875d8984916360ef315216667be6)), closes [#9](https://github.com/AtomiCloud/nix-registry/issues/9) [#11](https://github.com/AtomiCloud/nix-registry/issues/11)

## [4.2.0](https://github.com/AtomiCloud/nix-registry/compare/v4.1.0...v4.2.0) (2026-08-06)


### 🐛 Bug Fixes 🐛

* **drv:** make the releaser version guard name its reason ([8c0c390](https://github.com/AtomiCloud/nix-registry/commit/8c0c390349ff0b1db289f377c12b7f3eb8438985))


### ⬆️ Packages Updated ⬆️

* **patch:** retarget the releaser pin to v1.3.2 ([35c62a7](https://github.com/AtomiCloud/nix-registry/commit/35c62a7e7de64e0fa41e51dfd1a53ef9399cee13))

## [4.1.0](https://github.com/AtomiCloud/nix-registry/compare/v4.0.0...v4.1.0) (2026-08-06)


### 🐛 Bug Fixes 🐛

* **releaser:** compare the whole --version output, not a grep ([a10190c](https://github.com/AtomiCloud/nix-registry/commit/a10190c3c3db6598d9296582f95149c3f936cc7b)), closes [#59](https://github.com/AtomiCloud/nix-registry/issues/59)


### ✨ New Packages ✨

* **releaser:** package AtomiCloud offline release CLI ([a930915](https://github.com/AtomiCloud/nix-registry/commit/a930915f43d8cb8242725fa9ade6e5089d674219))


### ⬆️ Packages Updated ⬆️

* **releaser:** bump the pin to v1.2.0 and assert the built bytes ([570d84a](https://github.com/AtomiCloud/nix-registry/commit/570d84a5eb89216a801b6d636ff6d065cd9c8c51))
* **releaser:** move the pin to v1.3.0 ([749c78f](https://github.com/AtomiCloud/nix-registry/commit/749c78f030974a0ae0a8dd84e853ec75a2b829a9))

## [4.0.0](https://github.com/AtomiCloud/nix-registry/compare/v3.18.0...v4.0.0) (2026-08-06)


### ❌ Removed Packages ❌

* **dlint:** retire skills-fresh to its owning tool ([5975c54](https://github.com/AtomiCloud/nix-registry/commit/5975c5410130508343b8b5413e05fc942d24df6f))

## [3.18.0](https://github.com/AtomiCloud/nix-registry/compare/v3.17.0...v3.18.0) (2026-08-06)


### ✨ New Packages ✨

* **dlint:** read the configuration from dlint.yaml ([5b89b4d](https://github.com/AtomiCloud/nix-registry/commit/5b89b4d4526e8d1f8b5daeee1e422187ade8bf86))

## [3.17.0](https://github.com/AtomiCloud/nix-registry/compare/v3.16.0...v3.17.0) (2026-08-06)


### ✨ New Packages ✨

* **dlint:** add the no-custom-derivations check ([82ab995](https://github.com/AtomiCloud/nix-registry/commit/82ab9950cbeab55837f3d303857985fc22be21e9))

## [3.16.0](https://github.com/AtomiCloud/nix-registry/compare/v3.15.0...v3.16.0) (2026-08-06)


### ✨ New Packages ✨

* **dlint:** run several checks in one invocation ([349f910](https://github.com/AtomiCloud/nix-registry/commit/349f910011bcb7e58d20318a15a89a3476051847))
* **go:** ship go 1.26.5 from the registry ([1d941c4](https://github.com/AtomiCloud/nix-registry/commit/1d941c499e2b9678c1619fee15f94a995a1ba4b6))

## [3.15.0](https://github.com/AtomiCloud/nix-registry/compare/v3.14.0...v3.15.0) (2026-08-06)


### ✨ New Packages ✨

* **dlint:** add the workflow-policy check ([c58484a](https://github.com/AtomiCloud/nix-registry/commit/c58484ac29189c9e9288a570e9ed3a87b7dcf000))


### ⬆️ Packages Updated ⬆️

* **dlint:** scope toolchain-smoke per shell ([eac6ea0](https://github.com/AtomiCloud/nix-registry/commit/eac6ea0bf25b5352dd4b98d25073e5c9111841bc))

## [3.14.0](https://github.com/AtomiCloud/nix-registry/compare/v3.13.0...v3.14.0) (2026-08-05)

## [3.13.0](https://github.com/AtomiCloud/nix-registry/compare/v3.12.0...v3.13.0) (2026-08-05)


### 🐛 Bug Fixes 🐛

* **drv:** drop the phantom skopeo declaration from infrautils ([2020713](https://github.com/AtomiCloud/nix-registry/commit/2020713791cae567e1fefae86bfa19ee1df45f0d))
* **drv:** make dlint refuse a vacuous pass on every check ([0dc4b65](https://github.com/AtomiCloud/nix-registry/commit/0dc4b659d76cec0d34f4a7ba86a7ae27b1eef556)), closes [#63](https://github.com/AtomiCloud/nix-registry/issues/63)
* **drv:** resolve the harness dlint path without masking a failed cd ([8b90402](https://github.com/AtomiCloud/nix-registry/commit/8b9040246788c5a676d7d5e73225f01f8795b1f1)), closes [#63](https://github.com/AtomiCloud/nix-registry/issues/63)
* **drv:** stop CDPATH from redirecting the harness path resolution ([134a8b3](https://github.com/AtomiCloud/nix-registry/commit/134a8b3b5cb468eada8f02c10f38e45a99e0a9ec)), closes [#63](https://github.com/AtomiCloud/nix-registry/issues/63)


### ✨ New Packages ✨

* **dlint:** add four repo-agnostic repository linters ([f0d6901](https://github.com/AtomiCloud/nix-registry/commit/f0d69018d2059c2a2d81ef14ad6d95fc337a0e4e))


### ⬆️ Packages Updated ⬆️

* **infralint:** add skopeo ([e19042e](https://github.com/AtomiCloud/nix-registry/commit/e19042e5256c3de34ed88ec43c18057e5c67a21b))

## [3.12.0](https://github.com/AtomiCloud/nix-registry/compare/v3.11.0...v3.12.0) (2026-08-05)


### ⬆️ Packages Updated ⬆️

* **infralint:** add kubeconform and kyverno ([1465e42](https://github.com/AtomiCloud/nix-registry/commit/1465e42667f006618eec8a4829559d4764c60dd3))
* **atomiutils:** add ripgrep ([40adba7](https://github.com/AtomiCloud/nix-registry/commit/40adba72e878fa7dcd929df25de9b7cba483e266))

## [3.11.0](https://github.com/AtomiCloud/nix-registry/compare/v3.10.0...v3.11.0) (2026-07-26)


### ⬆️ Packages Updated ⬆️

* **cyanprint:** upgrade to sulfone.lite v4.9.2 ([98b4c57](https://github.com/AtomiCloud/nix-registry/commit/98b4c57f251ab5434b160ce885bb47bd201fcd25)), closes [AtomiCloud/sulfone.lite#26](https://github.com/AtomiCloud/sulfone.lite/issues/26)

## [3.10.0](https://github.com/AtomiCloud/nix-registry/compare/v3.9.0...v3.10.0) (2026-07-25)


### 🐛 Bug Fixes 🐛

* **cyanprint:** keep the Bun payload by disabling strip ([5d3b53e](https://github.com/AtomiCloud/nix-registry/commit/5d3b53e5416cd0aaa5907d8b6c9a569353c454e5))


### ⬆️ Packages Updated ⬆️

* **cyanprint:** upgrade to sulfone.lite v4.9.1 ([d995a5d](https://github.com/AtomiCloud/nix-registry/commit/d995a5db021fe62757f19cc226a888aefa0935b3)), closes [AtomiCloud/sulfone.lite#24](https://github.com/AtomiCloud/sulfone.lite/issues/24)

## [3.9.0](https://github.com/AtomiCloud/nix-registry/compare/v3.8.0...v3.9.0) (2026-07-24)


### ✨ New Packages ✨

* **mmoney-cli:** package Monarch Money CLI ([5aacc81](https://github.com/AtomiCloud/nix-registry/commit/5aacc813d6e479e859a4ed538d3909da75a1c955))

## [3.8.0](https://github.com/AtomiCloud/nix-registry/compare/v3.7.0...v3.8.0) (2026-07-23)


### 🐛 Bug Fixes 🐛

* **cyanprint:** autopatchelf so glibc is in the runtime closure ([4052a7b](https://github.com/AtomiCloud/nix-registry/commit/4052a7bd5486bb3c4540887ee39a6cafaea85ac6))
* **cyanprint:** guard autopatchelf/glibc to Linux only ([1c6716a](https://github.com/AtomiCloud/nix-registry/commit/1c6716ab9e1bd64e0dd9a0a39c24d4775132d60b))


### ✨ New Packages ✨

* **obsidian_headless:** package Obsidian headless sync CLI ([30f8fc5](https://github.com/AtomiCloud/nix-registry/commit/30f8fc55338f1a5af6d8c1211c513cdce2182e90))

## [3.7.0](https://github.com/AtomiCloud/nix-registry/compare/v3.6.0...v3.7.0) (2026-07-08)


### ✨ New Packages ✨

* **codemagic-cli-tools:** add codemagic-cli-tools Python package ([f961ff3](https://github.com/AtomiCloud/nix-registry/commit/f961ff32ce5931dae8a56411eedfd1a35597aa2a))

## [3.6.0](https://github.com/AtomiCloud/nix-registry/compare/v3.5.0...v3.6.0) (2026-07-07)


### ✨ New Packages ✨

* **nsc:** add Namespace Cloud CLI (nsc) ([1eab07d](https://github.com/AtomiCloud/nix-registry/commit/1eab07da3b985afa3a0f7431e49fb6d99f2c37ce))

## [3.5.0](https://github.com/AtomiCloud/nix-registry/compare/v3.4.0...v3.5.0) (2026-07-05)


### ⬆️ Packages Updated ⬆️

* **cyanprint:** source from sulfone.lite v4.5.0 via local binWrapper ([9fecfe9](https://github.com/AtomiCloud/nix-registry/commit/9fecfe91d8dd8431a434cea73e9b72410df16d9b))
* **cyanprint:** upgrade to sulfone.lite v4.7.0 ([9372ff9](https://github.com/AtomiCloud/nix-registry/commit/9372ff9b6d644d68a6d069f97eeeb709ac859ca0))

## [3.4.0](https://github.com/AtomiCloud/nix-registry/compare/v3.3.0...v3.4.0) (2026-07-04)


### ✨ New Packages ✨

* **dashboard-linter:** package grafana/dashboard-linter v0.2.0 ([b61cb47](https://github.com/AtomiCloud/nix-registry/commit/b61cb477792b7e5c0cc4e79dee0dc2196a53184f))

## [3.3.0](https://github.com/AtomiCloud/nix-registry/compare/v3.2.0...v3.3.0) (2026-06-30)


### ⬆️ Packages Updated ⬆️

* **dn-inspect:** support .slnx solutions and standalone project mode ([a7b0118](https://github.com/AtomiCloud/nix-registry/commit/a7b011817ed6900670237c9324272c8db348534e))

## [3.2.0](https://github.com/AtomiCloud/nix-registry/compare/v3.1.0...v3.2.0) (2026-06-30)


### ⬆️ Packages Updated ⬆️

* **dn-inspect:** configurable .NET SDK, default to .NET 10 ([483c01b](https://github.com/AtomiCloud/nix-registry/commit/483c01be738610aca56327bd5b465ffd103a2026))

## [3.1.0](https://github.com/AtomiCloud/nix-registry/compare/v3.0.0...v3.1.0) (2026-06-23)


### ⬆️ Packages Updated ⬆️

* **cyanprint:** upgrade to 2.22.0 ([098001f](https://github.com/AtomiCloud/nix-registry/commit/098001f1e5644ec139d78426bd9010d96405cc0e))

## [3.0.0](https://github.com/AtomiCloud/nix-registry/compare/v2.27.0...v3.0.0) (2026-06-20)


### ⚠ BREAKING CHANGES

* **darwin-intel:** x86_64-darwin (Intel macOS) is no longer a supported
system. Intel Mac users must stay on a release <= v2.27.0.

Claude-Session: https://claude.ai/code/session_01Rio8HNDX5jrx2T34Sn2UYa

### 🐛 Bug Fixes 🐛

* **ci:** drop attic from build job, keep parallelism cap ([bbba7e4](https://github.com/AtomiCloud/nix-registry/commit/bbba7e49e8bdae8bf071b7fccd864d14a123fbc5))
* **drv:** keep codecov pyinstaller archive intact on macos ([ac08805](https://github.com/AtomiCloud/nix-registry/commit/ac088055d7480dfcb1c28efc7883a05fa37faae1))
* **ci:** serialise nix build to one job to fit cold-cache build in 8GB ([2727c3c](https://github.com/AtomiCloud/nix-registry/commit/2727c3cd7b97015a3cfc60347b0ba3f57bd97033))
* **ci:** use attic and cap parallelism to avoid cold-cache OOM ([8906a16](https://github.com/AtomiCloud/nix-registry/commit/8906a167bb4aa6de94acf88f38747796b3772ca4))


### ⬆️ Packages Updated ⬆️

* **cyanprint:** upgrade to 2.21.1, unpin from 2.20.0 ([aa9f084](https://github.com/AtomiCloud/nix-registry/commit/aa9f084261da51225fb01626e9e34af132dada6c)), closes [#88](https://github.com/AtomiCloud/nix-registry/issues/88)


### ❌ Removed Packages ❌

* **darwin-intel:** drop x86_64-darwin (Intel macOS) support ([a46bb2d](https://github.com/AtomiCloud/nix-registry/commit/a46bb2d5cc7b0d7d1bdbf630132b84c89bb80d47))

## [2.27.0](https://github.com/AtomiCloud/nix-registry/compare/v2.26.0...v2.27.0) (2026-06-10)


### ⬆️ Packages Updated ⬆️

* **gardenio:** update garden to 0.14.20 ([cfbd805](https://github.com/AtomiCloud/nix-registry/commit/cfbd805aefdfdb29d052e9304338adc1731318f6))

## [2.26.0](https://github.com/AtomiCloud/nix-registry/compare/v2.25.0...v2.26.0) (2026-06-06)


### ⬆️ Packages Updated ⬆️

* all packages to 26.05 ([3be5e32](https://github.com/AtomiCloud/nix-registry/commit/3be5e32f280968a64c757872ac19129871997357))

## [2.25.0](https://github.com/AtomiCloud/nix-registry/compare/v2.24.0...v2.25.0) (2026-04-13)


### ✨ New Packages ✨

* **pagerduty-cli:** Add PagerDuty CLI for incident management ([0547add](https://github.com/AtomiCloud/nix-registry/commit/0547add87c99b2be076d52e3150b83877f6001fa))
* **pagerduty-cli:** Add PagerDuty CLI for incident management ([3365ab8](https://github.com/AtomiCloud/nix-registry/commit/3365ab8dd4cf1b6fff1e02a3ce549efb269d029d))
* **pagerduty-cli:** Add spec and planning docs ([1567f41](https://github.com/AtomiCloud/nix-registry/commit/1567f4175b874f7675fb8ceba6398fcd984708af))
* **pagerduty-cli:** Add spec and planning docs ([fdc9c7d](https://github.com/AtomiCloud/nix-registry/commit/fdc9c7dcef65bf77b8c1f5d3e31495d0077e3a3d))

## [2.24.0](https://github.com/AtomiCloud/nix-registry/compare/v2.23.0...v2.24.0) (2026-04-12)


### ✨ New Packages ✨

- **inspect:** Add inspect CLI for entity-level code review ([de079d8](https://github.com/AtomiCloud/nix-registry/commit/de079d87024c154e2fc85de010207c30d8767cb1))

## [2.23.0](https://github.com/AtomiCloud/nix-registry/compare/v2.22.0...v2.23.0) (2026-03-29)

### ⬆️ Packages Updated ⬆️

- **cyanprint:** Update CyanPrint to 2.20.0 in nix-registry ([7669c62](https://github.com/AtomiCloud/nix-registry/commit/7669c628a4973af05a6d70171ad93eee937bf334))

## [2.22.0](https://github.com/AtomiCloud/nix-registry/compare/v2.21.0...v2.22.0) (2026-03-23)

### 🐛 Bug Fixes 🐛

- address coderabbit local review findings ([16120dc](https://github.com/AtomiCloud/nix-registry/commit/16120dca610d8549a34692a1b1a756cd552f8a81))

### ✨ New Packages ✨

- **clickup-cli:** Add @krodak/clickup-cli for ClickUp task management ([4de7b2d](https://github.com/AtomiCloud/nix-registry/commit/4de7b2da339e912e6d74293fbb313151b5406f82))

## [2.21.0](https://github.com/AtomiCloud/nix-registry/compare/v2.20.0...v2.21.0) (2026-03-17)

### ⬆️ Packages Updated ⬆️

- **cyanprint:** update CyanPrint to latest version ([3f4d441](https://github.com/AtomiCloud/nix-registry/commit/3f4d4417368ab3b2c109babca25ef7e6bd8c53e6))

## [2.20.0](https://github.com/AtomiCloud/nix-registry/compare/v2.19.0...v2.20.0) (2026-03-16)

### ⬆️ Packages Updated ⬆️

- **cyanprint:** update CyanPrint to latest version ([df9f826](https://github.com/AtomiCloud/nix-registry/commit/df9f826f22b33dadd874fac37a1744dc19879a6b))

## [2.19.0](https://github.com/AtomiCloud/nix-registry/compare/v2.18.0...v2.19.0) (2026-03-15)

### ⬆️ Packages Updated ⬆️

- **cyanprint:** update CyanPrint to latest version ([d925467](https://github.com/AtomiCloud/nix-registry/commit/d9254678fb4cdc2f599a7405782b9e976be8f237))

## [2.18.0](https://github.com/AtomiCloud/nix-registry/compare/v2.17.0...v2.18.0) (2026-03-05)

### 🐛 Bug Fixes 🐛

- address coderabbit local review findings ([11e4fc3](https://github.com/AtomiCloud/nix-registry/commit/11e4fc3ae9ff41ad68a67a869023ae1211cd1556))
- **coderabbit:** address review feedback for md-mermaid-lint ([71ae23f](https://github.com/AtomiCloud/nix-registry/commit/71ae23fbc59c8934194f9d4949cd56176309af29))

### ✨ New Packages ✨

- **bunWrapper:** add md-mermaid-lint package to nix-registry ([a9faf83](https://github.com/AtomiCloud/nix-registry/commit/a9faf83aec90a26f04d648c27acecac5a1312175))
- **bunWrapper:** implement md-mermaid-lint package with CI integration ([5527df7](https://github.com/AtomiCloud/nix-registry/commit/5527df78fcd8d627129327855e1cac85cc199d48))

### Documentation Updates

- **spec:** add task spec for CU-86ewu2krt ([a80a43a](https://github.com/AtomiCloud/nix-registry/commit/a80a43a88ccd06650328d2aecb4bd9f48aaee3fa))
- **spec:** fix path references in plan-2.md ([2978d79](https://github.com/AtomiCloud/nix-registry/commit/2978d79e77b0b2577bc7f1f2558cc33702038718))

## [2.17.0](https://github.com/AtomiCloud/nix-registry/compare/v2.16.0...v2.17.0) (2026-02-27)

### 🐛 Bug Fixes 🐛

- **ccc:** address coderabbit local review findings [CU-86ewrbfra] ([e6077e6](https://github.com/AtomiCloud/nix-registry/commit/e6077e6cba8a1ebb2a4479d621e07335fe48601b))

### ✨ New Packages ✨

- **ccc:** chat with claude code on telegram [CU-86ewrbfra] ([eac106a](https://github.com/AtomiCloud/nix-registry/commit/eac106aa45c5f8b27624037443626b9ccefdfcd5))

### Documentation Updates

- add task spec for CU-86ewrbfra ([cae8b97](https://github.com/AtomiCloud/nix-registry/commit/cae8b97981727f55e7de071bd366e2d7cd3d6c71))
- mark acceptance criteria as complete [CU-86ewrbfra] ([da958fa](https://github.com/AtomiCloud/nix-registry/commit/da958fa477524455545e23e14ad6cd1e5227985e))

## [2.16.0](https://github.com/AtomiCloud/nix-registry/compare/v2.15.0...v2.16.0) (2026-02-25)

### 🐛 Bug Fixes 🐛

- **coderabbit:** change license from unfree to MIT [CU-86ewqguc4] ([e4ac687](https://github.com/AtomiCloud/nix-registry/commit/e4ac68718d8b86cd452cdd4744d4ed537d9d469d))

### ✨ New Packages ✨

- **coderabbit:** add coderabbit-cli as binWrapper package [CU-86ewqguc4] ([733b840](https://github.com/AtomiCloud/nix-registry/commit/733b840004e30ed06f4ca46a4028b0ae456b29a1))

## [2.15.0](https://github.com/AtomiCloud/nix-registry/compare/v2.14.0...v2.15.0) (2026-02-12)

### ✨ New Packages ✨

- **happy:** lets you control AI agents from anywhere ([41ad0b0](https://github.com/AtomiCloud/nix-registry/commit/41ad0b0ca86e2ba3258cec6c386439680ceb700e))

## [2.14.0](https://github.com/AtomiCloud/nix-registry/compare/v2.13.1...v2.14.0) (2026-02-05)

### ⬆️ Packages Updated ⬆️

- **dn-inspect:** v0.2.0 allowing include and no-build flag ([7208a14](https://github.com/AtomiCloud/nix-registry/commit/7208a14ae0e2bed4355f53beba17a81f3c931fa0))

## [2.13.1](https://github.com/AtomiCloud/nix-registry/compare/v2.13.0...v2.13.1) (2026-02-05)

### 🐛 Bug Fixes 🐛

- **mirrord:** incorrect darwin-arm slug ([38fd8e7](https://github.com/AtomiCloud/nix-registry/commit/38fd8e75b302ea7a18ee19fefe9de09be2c1a24b))

## [2.13.0](https://github.com/AtomiCloud/nix-registry/compare/v2.12.0...v2.13.0) (2026-02-05)

### 🐛 Bug Fixes 🐛

- **ci:** missing check for deadcode ([c841e21](https://github.com/AtomiCloud/nix-registry/commit/c841e212899acc51629d51170f15c6f1ddaa498d))

### ✨ New Packages ✨

- **dn-inspect:** friendly jetbrains analyzer for csharp ([80c6fe4](https://github.com/AtomiCloud/nix-registry/commit/80c6fe40f5011843a11de4a9ed2fd4e959572ea2))

## [2.12.0](https://github.com/AtomiCloud/nix-registry/compare/v2.11.0...v2.12.0) (2026-02-04)

### ✨ New Packages ✨

- **deadcode:** go deadcode wrapper that fails on deadcode findings ([2d703ab](https://github.com/AtomiCloud/nix-registry/commit/2d703ab1ab57abd7e42cbfdaeb4285ade77dbbfb))
- **deadcode:** go deadcode wrapper that fails on deadcode findings ([54e5d97](https://github.com/AtomiCloud/nix-registry/commit/54e5d972c304cd26b75d27e6bf47f60b5c9716f0))

## [2.11.0](https://github.com/AtomiCloud/nix-registry/compare/v2.10.0...v2.11.0) (2026-01-21)

### 🐛 Bug Fixes 🐛

- **ci:** incorrect worktrunk ci check ([be18d23](https://github.com/AtomiCloud/nix-registry/commit/be18d23fa800b91d60fc72f6ada05ac5bd191809))

### ✨ New Packages ✨

- **worktrunk:** CLI for git worktree management ([18466aa](https://github.com/AtomiCloud/nix-registry/commit/18466aab08506f5bb7382fc367113530455c40ca))

## [2.10.0](https://github.com/AtomiCloud/nix-registry/compare/v2.9.0...v2.10.0) (2026-01-21)

### ✨ New Packages ✨

- **cliproxyapi:** extend claudecode to use any model ([cf6c1e6](https://github.com/AtomiCloud/nix-registry/commit/cf6c1e6a97a5541b76ae2608c6860eba50696ef6))

## [2.9.0](https://github.com/AtomiCloud/nix-registry/compare/v2.8.0...v2.9.0) (2026-01-21)

### ⬆️ Packages Updated ⬆️

- all packages to 25.11 ([d60809e](https://github.com/AtomiCloud/nix-registry/commit/d60809ec1a4ec9c09e354ae3611a42192a6222c3))

## [2.8.0](https://github.com/AtomiCloud/nix-registry/compare/v2.7.0...v2.8.0) (2025-11-05)

### ⬆️ Packages Updated ⬆️

- **mirrord:** to 3.171.0 ([1d8b8ae](https://github.com/AtomiCloud/nix-registry/commit/1d8b8ae4d02f31be0a20bd91d0d292ec216ccc00))

## [2.7.0](https://github.com/AtomiCloud/nix-registry/compare/v2.6.1...v2.7.0) (2025-11-04)

### 🐛 Bug Fixes 🐛

- **drv:** incorrect hash ([fbf1151](https://github.com/AtomiCloud/nix-registry/commit/fbf115191b2b0e6bdc6ffb857cbae04bae6ee264))

### ⬆️ Packages Updated ⬆️

- **garden:** from 0.13.50 to 0.14.9 ([0c2cb63](https://github.com/AtomiCloud/nix-registry/commit/0c2cb639325f7b02e40be8dbeaef9762be7a1b94))

## [2.6.1](https://github.com/AtomiCloud/nix-registry/compare/v2.6.0...v2.6.1) (2025-06-25)

### 🐛 Bug Fixes 🐛

- upgrade infrastructure and upstream ([91d5f5c](https://github.com/AtomiCloud/nix-registry/commit/91d5f5ce5c4da945fb108870234d95b1454e4ec7))

## [2.6.0](https://github.com/AtomiCloud/nix-registry/compare/v2.5.0...v2.6.0) (2025-06-12)

### ✨ New Packages ✨

- **attic:** self-hosted nix binary cache ([71f3d52](https://github.com/AtomiCloud/nix-registry/commit/71f3d52dc1cc466b267b249b6f80c67aacfb3a07))

## [2.5.0](https://github.com/AtomiCloud/nix-registry/compare/v2.4.0...v2.5.0) (2025-06-10)

### 🐛 Bug Fixes 🐛

- **toml-cli:** incorrect cargo hash ([c10b156](https://github.com/AtomiCloud/nix-registry/commit/c10b1563094ee0b51208fba538bf4f71f6409333))

### ⬆️ Packages Updated ⬆️

- **cyanprint:** to v2.1.0 ([dbd864d](https://github.com/AtomiCloud/nix-registry/commit/dbd864d406da91e5843e3a0a57cfc16b2a68b8e8))

## [2.4.0](https://github.com/AtomiCloud/nix-registry/compare/v2.3.0...v2.4.0) (2025-05-07)

### ✨ New Packages ✨

- **openapi2postmanv2:** convert openapi to postman ([f47a429](https://github.com/AtomiCloud/nix-registry/commit/f47a429e0ac7ed2adf7082e9a473879fb47d2be6))

## [2.3.0](https://github.com/AtomiCloud/nix-registry/compare/v2.2.0...v2.3.0) (2025-04-23)

### ✨ New Packages ✨

- **dotnetlint:** recursively run dotnet lint ([f3fc876](https://github.com/AtomiCloud/nix-registry/commit/f3fc876bf6b14f98d403a99bcdd2fd1ccdd7b08c))
- **helmlint:** recursively run helm lint ([bb5ca77](https://github.com/AtomiCloud/nix-registry/commit/bb5ca77a4c92a59c844c11a8eda3ea0f9296dab5))

## [2.2.0](https://github.com/AtomiCloud/nix-registry/compare/v2.1.0...v2.2.0) (2025-04-13)

### ✨ New Packages ✨

- **codecov:** Codecov CLI tool for code coverage reporting ([40f3985](https://github.com/AtomiCloud/nix-registry/commit/40f398523f167c0330c6b949b785a9931d3e139a))

## [2.1.0](https://github.com/AtomiCloud/nix-registry/compare/v2.0.1...v2.1.0) (2025-02-15)

### ⬆️ Packages Updated ⬆️

- **default:** bump for all atomi updates ([e0c3e11](https://github.com/AtomiCloud/nix-registry/commit/e0c3e1137f7a769302ecf541e0a26625d42b3aed))
- **infrauitls:** to include skopeo ([86f11e2](https://github.com/AtomiCloud/nix-registry/commit/86f11e2b08ba43ebf546fb1d9fc462052123bba3))
- **atomiutils:** update atomiutils to use include toybox ([e555f87](https://github.com/AtomiCloud/nix-registry/commit/e555f87851050ae9e3deda96d7609fe4ef0d41d3))

## [2.0.1](https://github.com/AtomiCloud/nix-registry/compare/v2.0.0...v2.0.1) (2025-02-14)

### 🐛 Bug Fixes 🐛

- **infralint:** incorrect moving binary in install script ([e056b31](https://github.com/AtomiCloud/nix-registry/commit/e056b31e97d34b088e6cd8b067140eb9abb699a7))

### ⬆️ Packages Updated ⬆️

- **atomi_release:** add patch, minor, and major version update types ([5bbf7e4](https://github.com/AtomiCloud/nix-registry/commit/5bbf7e4dde01889f61a5387e376161d3adda55b9))
- **infrautils:** include opentofu ([c4a152f](https://github.com/AtomiCloud/nix-registry/commit/c4a152f1845f25b1f384f93a3a3ed53e29db337e))
- **atomiutils:** include tar, awk, gomplate ([e9218da](https://github.com/AtomiCloud/nix-registry/commit/e9218da74ffe00e1eda590b1a62c388a47aee329))
- **infralint:** include terraform tools ([69942b0](https://github.com/AtomiCloud/nix-registry/commit/69942b0c8f52bf24d5aa84f76e52c5b7aec2c318))

## [2.0.0](https://github.com/AtomiCloud/nix-registry/compare/v1.2.0...v2.0.0) (2025-02-02)

### ✨ New Packages ✨

- **infra:** infralint and infrautils, bundling of infra related tools ([0dd96d5](https://github.com/AtomiCloud/nix-registry/commit/0dd96d58808c6f68d2b2e5cb103c99f4d882e5d2))

### ⬆️ Packages Updated ⬆️

- **sg:** update sg to 0.11.0 ([c2097c8](https://github.com/AtomiCloud/nix-registry/commit/c2097c8f683c5c886f49b3f34b122d58b3418996))

## [1.2.0](https://github.com/AtomiCloud/nix-registry/compare/v1.1.0...v1.2.0) (2025-01-29)

### ✨ New Packages ✨

- **garden:** Automation for Kubernetes development and testing ([1674072](https://github.com/AtomiCloud/nix-registry/commit/1674072ca9c98e2be23efb32da13d9a4a9fc4df8))

## [1.1.0](https://github.com/AtomiCloud/nix-registry/compare/v1.0.5...v1.1.0) (2025-01-29)

### ✨ New Packages ✨

- **atomiutils:** extend version of coreutils for atomicloud ([0df3c5a](https://github.com/AtomiCloud/nix-registry/commit/0df3c5a5f15f11f5f9b967db783b9bea018cb001))

## [1.0.5](https://github.com/AtomiCloud/nix-registry/compare/v1.0.4...v1.0.5) (2025-01-28)

### 🐛 Bug Fixes 🐛

- **drv:** include cyanprint back into registry ([7aea611](https://github.com/AtomiCloud/nix-registry/commit/7aea611df762f100156532eeade6d3f699deaf1e))

## [1.0.4](https://github.com/AtomiCloud/nix-registry/compare/v1.0.3...v1.0.4) (2025-01-28)

### 🐛 Bug Fixes 🐛

- **drv:** temp rm cyanprint to remove ref to old atomi ([b2e7f50](https://github.com/AtomiCloud/nix-registry/commit/b2e7f5026b4a83ad04ffb6b3332071229847a6b7))

## [1.0.3](https://github.com/AtomiCloud/nix-registry/compare/v1.0.2...v1.0.3) (2025-01-22)

### 🐛 Bug Fixes 🐛

- **drv:** self reference to remove dependency on old atomi ([1cb3ab0](https://github.com/AtomiCloud/nix-registry/commit/1cb3ab060a9be7ed6a4a665aec0ab05ea9329d20))

## [1.0.2](https://github.com/AtomiCloud/nix-registry/compare/v1.0.1...v1.0.2) (2025-01-09)

### 🐛 Bug Fixes 🐛

- check pkgs ([9d0be4a](https://github.com/AtomiCloud/nix-registry/commit/9d0be4a230b5153a288f817543383e01475bc24a))
- ci failing ([aee3a40](https://github.com/AtomiCloud/nix-registry/commit/aee3a409eb88f0a17350e826ce818d9614ef0fde))
- improve CI to check build ([56ece41](https://github.com/AtomiCloud/nix-registry/commit/56ece417b5f1f53b6fe5fdfacd8d6e30c9fc29cd))

## [1.0.1](https://github.com/AtomiCloud/nix-registry/compare/v1.0.0...v1.0.1) (2025-01-09)

### 🐛 Bug Fixes 🐛

- sg incorrect binding ([1db974d](https://github.com/AtomiCloud/nix-registry/commit/1db974d33ee410c6916befbb1dfd56e312bc6a83))

## 1.0.0 (2025-01-04)

### 🐛 Bug Fixes 🐛

- prettier styling ([cf43f73](https://github.com/AtomiCloud/nix-registry/commit/cf43f73b71b8fa213fff11de2976efa15f75edc4))

### ✨ New Packages ✨

- initial commit ([cbaaa38](https://github.com/AtomiCloud/nix-registry/commit/cbaaa38b3e596bd6fd92ed5d05b45a39ed3f8ba6))
