# Testify v2.4 Roadmap

## Vision Statement

  v2.4 will mark the end of our "experimental" phase (2.1, 2.2, 2.3, 2.4) when we indulge into breaking changes from previous versions.
  After 2.4, our API will abide by semver to the letter.

  v2.4 will still require go1.24 and may introduce some features only available with go1.25 (e.g. synctest).
 
  v2.5 will require go1.25 and make generally available features that require go1.25.

## Features

  New features:

   * ✅ **NoFileDescriptorLeak (Unix)** [🧪⚠️] (Phase 3.2)
   * ✅ Failing generated examples for NoFileDescriptorLeak on macos and windows
   * ✅ **Migration tool**
   * ✅ **EventuallyT[T]** [🧪] (Phase 2.3) (with support for returned error)
   * ✅ **EventuallyWithContextT[T]** [🧪] (Phase 2.4)
   * ✅ **Consistently** [🧪] (Phase 2.4)

## Fixes

   * None known yet

## Documentation

   📝 **Configure doc site versioning** [📚🛠️] (requires change in shared release workflow)
   🧪 **News section for doc site** [📚]
   ♥️ fix remaining few typos [📚]

### Doc generation

   ♥️ Render multiple examples from annotated values: multiple success or failure values

### RICHER CONTENT

   ♥️ **Educational content expansion** [📚]
   ♥️ Showcase best practices
   ♥️ difflib doc could borrow more from the updated source https://docs.python.org/3/library/difflib.html#module-difflib [📚]

### HUGO-RELATED

   ♥️ **Improve navigation for large documentation pages** [📚🎨]
   ♥️ **Assertion index side-bar**/**All core assertions index**/**All generics index** [📚🎨]

## CI

   ♥️ CI: Run broken link check [📚]
   ♥️ CI: Run go code snippets formating (mdsf) [📚]
   ♥️ run mdsf in CI / githook [📚] (attention! may break imports) (ci-worflows project)
   ♥️ CI: automate doc contributors update for doc site [📚] (ci-worflows project)
   ♥️ CI: re-instate past project with doc spellcheck & markdownlint [📚] (ci-workflows project)

## MAINTAINABILITY

   ♥️ **Remove extraneous helper functions**
     * See if we can create a third package with _just_ helpers
   ♥️ Function types (e.g., `ValueAssertionFunc`, `PanicAssertionFunc`) would need some proper comment for categorization. [📚]

## TESTS

   ✅ Complete coverage for enable/stubs [🏁]
   ♥️ **Improve test coverage for generated helpers** [🏁]
   ♥️ Code generator testing using golden file. Currently the generator integration test is merely a smoke test. 

## PERF

   ♥️ **Difflib memory allocation optimization** [⚡]
   ♥️ graphic visualization for benchmarks [📚]

## Backlog from v2.3

 * Won't do
   ⛔ [📚] ~Replace or extend "Usage:" sections in godoc with references to generated examples (not sure yet this will actually improve anything)~
   ⛔ fuzz test in CI reported an error: fix it (issue: CI doesn't upload the failing case, only the corpus. Need to fix CI first)
      - can't reproduce with a 10x longer, 10x larger test sample
      - Most likely, CI did hit a timeout ("hang" failure) when overloaded with tests.
   ⛔ **Implement Result[T] Pattern** [🧪] (Phase 1.2)
   ⛔ **Update codegen for Result[T] returns** [🛠️]
   ⛔ doc: use hugo file resource to import go code in markdown (easier to format source/alternative to mdsf) [📚]
   ⛔ **Custom card shortcode for doc site** [📚]
   ⛔ **custom JSON serializer injection**

## Plans for v2.5

   📝 **Eventually and all -> synctest** [🧪⚠️]
   📝 **NoFileDescriptorLeak (macos)** [🧪⚠️]
   📝 **NoFileDescriptorLeak (Windows)** [🧪⚠️]
   📝 **Upstream PR candidates** (monitoring)
   ♥️ **JSON/YAML equivalence vs equality**
   * ♥️ **JSONPointerT[T]** [🧪] (Phase 4.1)
   * ♥️ **Better time comparison assertions**
   * ❌ NoFileDescriptorLeak API for main?
   * 📝 export internal tools (spew, difflib)

# TOOLING

   ✅ Innovation (AI-aided development, skills with advanced go patterns)
   ✅ Running codegen with "go generate" does not produce exactly the same result as when running the binary from ./codegen:
      tool and headers are empty.

   ✅ Benchmark viz: a tool to generate nice charts from go benchmark output
   ✅ go-fred MCP
      * ✅ godoc-check with agent:
           a MCP tool that runs a pipeline with hunspell (as WASM), godoc-specific filters and a bulk
           in-place comment update tool
      * ✅ test callgraph
      * ✅ go modules mappings to folders (fast cartography of a go monorepo)
      * ✅ detailed coverage analysis
      * ✅ markdown spellcheck & linting
   ✅ godoc-check SDK agent
   📝 markdown-check SDK agent
   📝 benchviz SDK agent
   ♥️ git-janitor: a tool to monitor my git repos of interest (forks, clones, actively contributing, stale...) and performs janitorial tasks
   ♥️ go4us: TBD
      * auto-fix feature for funcorder, acting as a custom formatter for golangci-lint, with Fred's ordering rules


> [!NOTE]
>
> CLAUDE seems to like unicode emojis a lot (sign of the times, perhaps).
>
> Let's make it happy and adopt the following conventions.
>
> Current status symbols:
>
> * ✅, [x]: Stand for DONE, COMPLETE or MOSTLY COMPLETE
> * ⏳, []:  Stand for IN PROGRESS
> * ❌ Stands for ISSUE or CONCERN
>
> Categorization symbols:
>
> * 🛠️ Stands for INTERNAL TOOLING
> * 🏁 Stands for TESTING
> * 📚 Stands for DOCUMENTATION
> * 😇 Stands for COMPLIANCE
>
> Prioritization symbols:
>
> * 🔍 Stands for NEED INVESTIGATION (before acting)
> * ⛔ Stands for WONT'DO (usually with the reason why)
> * 🔥 Stands for URGENT (requires immediate action)
> * ⚠️ Stands for NEED ATTENTION (requires swift action)
> * ♥️ Stands for ENHANCEMENT (would love it)
> * 🎨 Stands for COSMETIC, LAYOUT, VISUAL IMPROVEMENT
> * 🧪 Stands for EXPERIMENTAL
> * ⚡ Stands for PERFORMANCE RELATED
> * 📝 Stands for PLANNED
>
> Qualitative assessment symbols and terms:
>
> * ⭐⭐⭐: first-class achievement (outstanding, brilliant, A+)
> * ⭐⭐: great achievement (great, excellent, A)
> * ⭐: subpar achievement (decent, correct, B, B-)
> * 👎: not good (inadequate, wrong, C)
>
