<script>
  export let appVersion = ''

  const githubUrl = 'https://github.com/phat7000/OneBoardSafePaste'
  const websiteUrl = 'https://oneboard.io.vn'
  const upstreamUrl = 'https://github.com/AlexanderMckain/Silo-Redact'
</script>

<div
  class="about-root text-left text-zinc-200 antialiased selection:bg-cyan-500/30 selection:text-white"
  aria-label="About OneBoard Safe Paste"
>
  <header class="mb-8 border-b border-silo-border pb-6" id="top">
    <p class="mb-1 font-mono text-xs font-semibold uppercase tracking-widest text-silo-accent">
      OneBoard Safe Paste
    </p>
    <h1 class="font-sans text-xl font-semibold leading-snug text-white sm:text-2xl">
      Protect sensitive information before it leaves your clipboard.
    </h1>
  </header>

  <section class="mb-8 space-y-3 font-sans text-sm leading-relaxed text-zinc-300">
    <p>
      OneBoard Safe Paste is a local-first Windows privacy tool for redacting secrets, credentials, and personal
      information before text is pasted into AI assistants, support portals, tickets, chat systems, email, or web
      forms.
    </p>
    <p>
      Processing is performed locally on your device. Core redaction does not require an account, activation, a
      cloud scrubbing service, or an Internet connection.
    </p>
    <p class="text-xs text-silo-muted">
      Redaction is rule-based and may not detect every sensitive value. Review sanitized output before sharing it.
    </p>
  </section>

  <section
    class="mb-8 rounded-lg border border-cyan-900/50 bg-cyan-950/30 px-4 py-3 font-mono text-xs leading-relaxed text-cyan-100/90"
    aria-label="Privacy model summary"
  >
    <h2 class="mb-2 font-sans text-xs font-semibold uppercase tracking-wide text-cyan-400">
      Local-first privacy model
    </h2>
    <ul class="list-inside list-disc space-y-1.5 text-cyan-100/80">
      <li>Clipboard and workspace text is processed on-device by the local redaction engine.</li>
      <li>No telemetry, analytics, account system, or cloud redaction API is required.</li>
      <li>You decide what leaves the machine after reviewing the sanitized result.</li>
    </ul>
  </section>

  <section class="mb-10" aria-labelledby="capabilities-heading">
    <h2
      id="capabilities-heading"
      class="mb-3 font-mono text-xs font-semibold uppercase tracking-widest text-silo-muted"
    >
      Core capabilities
    </h2>
    <div class="grid gap-3 sm:grid-cols-3">
      <article class="rounded-md border border-silo-border bg-zinc-900/50 p-3 font-sans">
        <h3 class="mb-1 text-sm font-semibold text-white">Sensitive-data redaction</h3>
        <p class="text-xs leading-relaxed text-zinc-400">
          Built-in credential and PII rules help sanitize clipboard text, logs, and diagnostics.
        </p>
      </article>
      <article class="rounded-md border border-silo-border bg-zinc-900/50 p-3 font-sans">
        <h3 class="mb-1 text-sm font-semibold text-white">Ghost Mode</h3>
        <p class="text-xs leading-relaxed text-zinc-400">
          Background clipboard monitoring can redact matched content before the next paste.
        </p>
      </article>
      <article class="rounded-md border border-silo-border bg-zinc-900/50 p-3 font-sans">
        <h3 class="mb-1 text-sm font-semibold text-white">Custom rules</h3>
        <p class="text-xs leading-relaxed text-zinc-400">
          Add custom regular expressions when the built-in rule library does not cover your workflow.
        </p>
      </article>
    </div>
  </section>

  <section class="mb-10 border-t border-silo-border pt-8 font-sans" aria-labelledby="product-heading">
    <h2
      id="product-heading"
      class="mb-2 font-mono text-xs font-semibold uppercase tracking-widest text-silo-muted"
    >
      Product
    </h2>
    <div class="flex flex-wrap items-center gap-2 text-xs">
      {#if appVersion}
        <span class="rounded bg-zinc-800 px-2 py-0.5 font-mono text-zinc-400">v{appVersion}</span>
      {/if}
      <span class="rounded border border-silo-accent/50 bg-silo-accent/10 px-2 py-0.5 font-medium text-silo-accent">
        Open Source
      </span>
    </div>
    <div class="mt-4 flex flex-wrap gap-x-4 gap-y-2 text-sm">
      <a href={websiteUrl} target="_blank" rel="noopener noreferrer" class="text-silo-accent hover:underline">
        Website
      </a>
      <a href={githubUrl} target="_blank" rel="noopener noreferrer" class="text-silo-accent hover:underline">
        Source on GitHub
      </a>
    </div>
  </section>

  <section class="mb-10 border-t border-silo-border pt-8 font-sans" aria-labelledby="upstream-heading">
    <h2
      id="upstream-heading"
      class="mb-2 font-mono text-xs font-semibold uppercase tracking-widest text-silo-muted"
    >
      Upstream / Attribution
    </h2>
    <p class="text-sm leading-relaxed text-zinc-300">
      OneBoard Safe Paste is derived from
      <a href={upstreamUrl} target="_blank" rel="noopener noreferrer" class="text-silo-accent hover:underline">
        Silo-Redact
      </a>
      by Alexander McKain. The original MIT License and copyright notice are preserved in the source repository.
    </p>
  </section>

  <section class="border-t border-silo-border pt-8 font-sans" aria-labelledby="license-heading">
    <h2
      id="license-heading"
      class="mb-2 font-mono text-xs font-semibold uppercase tracking-widest text-silo-muted"
    >
      License and privacy
    </h2>
    <p class="text-sm leading-relaxed text-zinc-300">
      This software is distributed under the MIT License. Core local functionality does not upload clipboard or
      workspace content for processing. Your operating system and applications you paste into may have their own
      network behavior and privacy policies.
    </p>
  </section>
</div>
