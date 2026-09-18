<script lang="ts">
  import { onMount } from 'svelte';
  import {
    Copy,
    Download,
    Pause,
    Play,
    RefreshCw,
    Search,
    ScrollText,
    FileText
  } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import { Switch } from '$lib/components/ui/switch/index.js';
  import * as Dialog from '$lib/components/ui/dialog/index.js';
  import * as Tabs from '$lib/components/ui/tabs/index.js';
  import * as Select from '$lib/components/ui/select/index.js';
  import type { ManagementClient, ManagementConfig, RequestLogFile } from '$lib/api';
  import {
    LOG_LEVELS,
    LOG_PAGE_SIZE,
    LOG_FETCH_SIZE,
    LOG_POLL_INTERVAL,
    fileSize,
    filterLogs,
    logLevel,
    requestIDMatches,
    type LogEntry
  } from '$lib/logs';

  let {
    client,
    config = {},
    disabled = false,
    onrefresh = async () => {}
  }: {
    client: ManagementClient;
    config?: ManagementConfig;
    disabled?: boolean;
    onrefresh?: () => Promise<void>;
  } = $props();
  let tab = $state('server');
  let entries = $state<LogEntry[]>([]);
  let cursor = $state('');
  let loaded = $state(false);
  let busy = $state(false);
  let paused = $state(false);
  let catchingUp = $state(false);
  let error = $state('');
  let notice = $state('');
  let lastUpdated = $state('');
  let query = $state('');
  let severity = $state('all');
  let wrap = $state(true);
  let page = $state(0);
  let sequence = 0;
  let controller: AbortController;
  let alive = false;
  const loggingDisabled = $derived(config['logging-to-file'] === false);
  const filtered = $derived(filterLogs(entries, query, severity));
  const pageCount = $derived(Math.max(1, Math.ceil(filtered.length / LOG_PAGE_SIZE)));
  const currentPage = $derived(Math.min(page, pageCount - 1));
  const visible = $derived(
    filtered.slice(currentPage * LOG_PAGE_SIZE, (currentPage + 1) * LOG_PAGE_SIZE)
  );
  let files = $state<RequestLogFile[]>([]);
  let filesLoaded = $state(false);
  let filesBusy = $state(false);
  let filesError = $state('');
  let fileQuery = $state('');
  let requestID = $state('');
  const filteredFiles = $derived(
    files.filter((file) => file.name.toLowerCase().includes(fileQuery.toLowerCase()))
  );
  let filePage = $state(0);
  const filePageCount = $derived(Math.max(1, Math.ceil(filteredFiles.length / 50)));
  const currentFilePage = $derived(Math.min(filePage, filePageCount - 1));
  let detailOpen = $state(false);
  let detailTitle = $state('');
  let detailText = $state('');
  let detailFile = $state('');
  let detailSize = $state(0);
  let detailOffset = $state(0);
  let detailMore = $state(false);
  let detailModified = $state(0);
  let detailBusy = $state(false);
  let detailError = $state('');
  let detailNotice = $state('');
  let previewController: AbortController | undefined;
  const message = (cause: unknown) =>
    cause instanceof Error ? cause.message : 'Unable to load logs. Please try again.';

  async function load(latest = false) {
    if (busy || disabled || loggingDisabled || !alive) return;
    busy = true;
    error = '';
    notice = '';
    try {
      do {
        const requestCursor = !latest ? cursor : '';
        const limit = requestCursor ? LOG_FETCH_SIZE : LOG_PAGE_SIZE;
        const data = await client.getLogs(
          { limit, ...(requestCursor ? { cursor: requestCursor } : {}) },
          controller.signal
        );
        if (!alive) return;
        const more = Boolean(requestCursor) && data.lines.length >= limit && !data['cursor-reset'];
        if (more && (!data['next-cursor'] || data['next-cursor'] === requestCursor))
          throw new Error(
            'Log cursor did not advance. Live updates are paused; reload the latest logs to recover.'
          );
        // Follow the end only if the operator is still there when this read completes.
        const wasLastPage = currentPage === pageCount - 1;
        const additions = data.lines.map((text) => ({
          id: sequence++,
          text,
          level: logLevel(text)
        }));
        if (latest || !loaded) {
          entries = additions;
          page = 0;
        } else {
          if (data['cursor-reset']) {
            additions.unshift({
              id: sequence++,
              text: 'Log cursor reset: the server rotated or cleared logs. Latest available lines follow; earlier loaded history is retained and may overlap.',
              level: 'other',
              reset: true
            } as LogEntry);
            paused = true;
            notice =
              'The log cursor reset. Review the boundary in loaded history, then resume when ready.';
          }
          entries = [...entries, ...additions];
          if (wasLastPage)
            page = Math.max(
              0,
              Math.ceil(filterLogs(entries, query, severity).length / LOG_PAGE_SIZE) - 1
            );
        }
        cursor = data['next-cursor'];
        loaded = true;
        catchingUp = more;
        if (!more) lastUpdated = new Date().toLocaleTimeString();
        if (!more) break;
        // Yield between serialized batches so pause, navigation and rendering stay responsive.
        await new Promise<void>((resolve) => setTimeout(resolve, 0));
      } while (
        alive &&
        !paused &&
        !disabled &&
        !loggingDisabled &&
        tab === 'server' &&
        document.visibilityState !== 'hidden'
      );
    } catch (cause) {
      if (!controller.signal.aborted) {
        error = message(cause);
        paused = true;
      }
    } finally {
      if (alive) busy = false;
    }
  }
  async function togglePaused() {
    paused = !paused;
    if (!paused) await load();
  }
  async function enableLogging() {
    if (busy || disabled) return;
    busy = true;
    error = '';
    try {
      await client.setBoolean('logging-to-file', true, controller.signal);
      await onrefresh();
    } catch (cause) {
      if (!controller.signal.aborted) error = message(cause);
    } finally {
      if (alive) busy = false;
    }
  }
  async function loadFiles() {
    if (filesBusy || disabled || !alive) return;
    filesBusy = true;
    filesError = '';
    try {
      const data = await client.listRequestLogs(controller.signal);
      if (alive) {
        files = data;
        filesLoaded = true;
      }
    } catch (cause) {
      if (!controller.signal.aborted) filesError = message(cause);
    } finally {
      if (alive) filesBusy = false;
    }
  }
  async function preview(name: string, more = false) {
    previewController?.abort();
    const request = new AbortController();
    previewController = request;
    if (!more) {
      detailTitle = name;
      detailFile = name;
      detailText = '';
      detailOffset = 0;
      detailSize = 0;
      detailMore = false;
      detailModified = 0;
    }
    detailOpen = true;
    detailNotice = '';
    detailBusy = true;
    detailError = '';
    try {
      const data = await client.getRequestLogPreview(
        name,
        { offset: more ? detailOffset : 0, limit: 65536 },
        request.signal
      );
      if (request.signal.aborted || !alive) return;
      if (more && (data.modified !== detailModified || data.size < detailOffset)) {
        detailError =
          'This file changed while you were reading it. Reload its preview to avoid mixing versions.';
        detailMore = false;
        return;
      }
      detailText = more ? detailText + data.text : data.text;
      detailOffset = data.next_offset;
      detailSize = data.size;
      detailMore = data.has_more;
      detailModified = data.modified;
    } catch (cause) {
      if (!request.signal.aborted) detailError = message(cause);
    } finally {
      if (!request.signal.aborted && alive) detailBusy = false;
    }
  }
  function inspect(entry: LogEntry) {
    previewController?.abort();
    detailFile = '';
    detailTitle = 'Log line details';
    detailNotice = '';
    detailText = entry.text;
    detailError = '';
    detailBusy = false;
    detailMore = false;
    detailOpen = true;
  }
  function lookup() {
    const matches = files.filter((file) => requestIDMatches(file.name, requestID));
    if (!matches.length) {
      filesError =
        'No saved log matches this request ID. Refresh the file list or check whether request logging was enabled for that request.';
      return;
    }
    if (matches.length > 1) {
      fileQuery = requestID.trim();
      filesError = 'Multiple saved logs match this request ID. Choose a file from the list.';
      return;
    }
    filesError = '';
    void preview(matches[0].name);
  }
  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      if (detailOpen) detailNotice = 'Copied to clipboard.';
      else notice = 'Copied to clipboard.';
    } catch {
      if (detailOpen)
        detailNotice =
          'Clipboard access was unavailable. Select the text or download the file instead.';
      else notice = 'Clipboard access was unavailable. Download the logs instead.';
    }
  }
  function downloadVisible() {
    const url = URL.createObjectURL(
      new Blob([visible.map((entry) => entry.text).join('\n') + '\n'], {
        type: 'text/plain;charset=utf-8'
      })
    );
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = 'server-logs-visible.txt';
    anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  onMount(() => {
    alive = true;
    controller = new AbortController();
    void load();
    const timer = setInterval(() => {
      if (
        tab === 'server' &&
        !paused &&
        !disabled &&
        !loggingDisabled &&
        document.visibilityState !== 'hidden'
      )
        void load();
    }, LOG_POLL_INTERVAL);
    return () => {
      alive = false;
      clearInterval(timer);
      controller.abort();
      previewController?.abort();
    };
  });
  $effect(() => {
    if (tab === 'requests' && !filesLoaded && !filesBusy && !filesError && alive) void loadFiles();
  });
  $effect(() => {
    if (!detailOpen) previewController?.abort();
  });
</script>

<section class="panel logs-panel" aria-label="Log inspection">
  <div class="panel-header">
    <div class="section-heading">
      <h2>Logs</h2>
      <p class="muted">Inspect application activity and saved request details.</p>
    </div>
  </div>
  <Tabs.Root bind:value={tab} class="flex-col min-w-0">
    <Tabs.List class="log-tabs mx-6 mb-4 max-w-full"
      ><Tabs.Trigger value="server">Server logs</Tabs.Trigger><Tabs.Trigger value="requests"
        >Request logs</Tabs.Trigger
      ></Tabs.List
    >
    <Tabs.Content value="server" class="min-w-0">
      <div class="toolbar">
        <div class="search">
          <Search size={16} /><Input
            aria-label="Search logs"
            placeholder="Search loaded logs…"
            bind:value={query}
            oninput={() => (page = 0)}
          />
        </div>
        <div class="severity">
          <span>Severity</span>
          <Select.Root
            type="single"
            bind:value={severity}
            onValueChange={() => (page = 0)}
            items={[
              { value: 'all', label: 'All levels' },
              ...LOG_LEVELS.map((level) => ({ value: level, label: level }))
            ]}
          >
            <Select.Trigger aria-label="Log severity"
              ><Select.Value placeholder="All levels" /></Select.Trigger
            >
            <Select.Content
              ><Select.Item value="all" label="All levels">All levels</Select.Item
              >{#each LOG_LEVELS as level}<Select.Item value={level} label={level}
                  >{level}</Select.Item
                >{/each}</Select.Content
            >
          </Select.Root>
        </div>
        <Button
          variant="ghost"
          disabled={!query && severity === 'all'}
          onclick={() => {
            query = '';
            severity = 'all';
            page = 0;
          }}>Clear filters</Button
        >
        <label class="wrap-control"
          ><Switch aria-label="Wrap log lines" bind:checked={wrap} />Wrap lines</label
        >
      </div>
      <div class="toolbar compact">
        <Button variant="outline" disabled={disabled || loggingDisabled} onclick={togglePaused}
          >{#if paused}<Play size={14} />Resume live{:else}<Pause size={14} />Pause live{/if}</Button
        >
        <Button
          variant="outline"
          disabled={disabled || busy || loggingDisabled}
          onclick={() => load()}><RefreshCw size={14} />Refresh logs</Button
        >
        <Button
          variant="ghost"
          disabled={disabled || busy || loggingDisabled}
          onclick={() => load(true)}>Load latest (replace history)</Button
        >
        <Badge variant="outline"
          >{loggingDisabled
            ? 'Logging disabled'
            : paused
              ? 'Paused'
              : catchingUp
                ? 'Catching up…'
                : busy
                  ? 'Loading'
                  : 'Live · 5 seconds'}</Badge
        >
      </div>
      {#if error}<div class="error-banner log-message" role="alert">
          {error}{loaded ? ' Loaded lines are retained; live updates are paused.' : ''}
        </div>{/if}
      {#if notice}<p role="status" class="log-message muted">{notice}</p>{/if}
      {#if loggingDisabled}<div class="empty-state">
          <ScrollText size={26} />
          <h3>File logging is disabled</h3>
          <p>Enable application file logging to inspect server activity here.</p>
          <Button variant="constructive" disabled={disabled || busy} onclick={enableLogging}
            >Enable file logging</Button
          >
        </div>
      {:else if !loaded && busy}<p role="status" class="empty-state">Loading server logs…</p>
      {:else if !filtered.length}<div class="empty-state">
          <ScrollText size={26} />
          <h3>{query || severity !== 'all' ? 'No matching log lines' : 'No logs available'}</h3>
          <p>
            {query || severity !== 'all'
              ? 'Try another search or clear the filters.'
              : 'New application activity will appear when the server writes it.'}
          </p>
        </div>
      {:else}
        <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
        <div
          class="log-lines"
          class:nowrap={!wrap}
          tabindex="0"
          role="region"
          aria-label="Log output"
          aria-busy={busy}
        >
          {#each visible as entry (entry.id)}
            <button
              class="log-row"
              class:reset={entry.reset}
              onclick={() => inspect(entry)}
              aria-label={`Inspect log line ${entry.id + 1}`}
              ><span class="line-number" aria-hidden="true">{entry.id + 1}</span><span
                class="level"
                data-level={entry.level}>{entry.level}</span
              ><span class="line-text">{entry.text}</span></button
            >
          {/each}
        </div>
      {/if}
      <div class="log-footer">
        <div class="muted">
          {filtered.length} matching / {entries.length} loaded lines{lastUpdated
            ? ` · Updated ${lastUpdated}`
            : ''}<br />Initial view loads the latest {LOG_PAGE_SIZE} lines. Loaded history stays until
          you leave this section or replace it.
        </div>
        <div class="actions">
          <Button
            variant="outline"
            disabled={currentPage === 0}
            onclick={() => (page = currentPage - 1)}>Previous lines</Button
          ><span>Page {currentPage + 1} of {pageCount}</span><Button
            variant="outline"
            disabled={currentPage >= pageCount - 1}
            onclick={() => (page = currentPage + 1)}>Next lines</Button
          ><Button
            variant="ghost"
            disabled={!visible.length}
            onclick={() => copy(visible.map((entry) => entry.text).join('\n'))}
            ><Copy size={14} />Copy visible</Button
          ><Button variant="ghost" disabled={!visible.length} onclick={downloadVisible}
            ><Download size={14} />Download visible</Button
          >
        </div>
      </div>
    </Tabs.Content>
    <Tabs.Content value="requests" class="min-w-0">
      <div class="toolbar">
        <div class="search">
          <Search size={16} /><Input
            aria-label="Search request log files"
            placeholder="Search file names…"
            bind:value={fileQuery}
            oninput={() => (filePage = 0)}
          />
        </div>
        <Button variant="outline" disabled={disabled || filesBusy} onclick={loadFiles}
          ><RefreshCw size={14} />Refresh files</Button
        >
      </div>
      <form
        class="toolbar compact"
        onsubmit={(event) => {
          event.preventDefault();
          lookup();
        }}
      >
        <Input aria-label="Request ID" placeholder="Request ID" bind:value={requestID} /><Button
          type="submit"
          variant="outline"
          disabled={disabled || filesBusy || !filesLoaded || !requestID.trim()}>Find request</Button
        >
      </form>
      <p class="muted log-message">
        {config['request-log']
          ? 'Request logging is enabled. Saved request and error files are listed below.'
          : 'Full request logging is disabled. Saved files remain available; failed requests may still create error logs.'}
        Files can contain request or response content.
      </p>
      {#if filesError}<div role="alert" class="error-banner log-message">{filesError}</div>{/if}
      {#if filesBusy && !filesLoaded}<p role="status" class="empty-state">
          Loading saved request logs…
        </p>{:else if !filteredFiles.length}<div class="empty-state">
          <FileText size={26} />
          <h3>{fileQuery ? 'No matching files' : 'No saved request logs'}</h3>
          <p>
            {fileQuery
              ? 'Try a different file name.'
              : 'Only files currently stored by the server appear here.'}
          </p>
        </div>{:else}
        <ul class="file-list" aria-label="Saved request log files">
          {#each filteredFiles.slice(currentFilePage * 50, (currentFilePage + 1) * 50) as file (file.name)}<li
            >
              <div class="file-info">
                <strong>{file.name}</strong><span class="muted"
                  >{fileSize(file.size)} · {new Date(file.modified * 1000).toLocaleString()} · {file.kind}</span
                >
              </div>
              <div class="actions">
                <Button
                  variant="outline"
                  {disabled}
                  onclick={() => preview(file.name)}
                  aria-label={`Preview ${file.name}`}>Preview</Button
                ><Button
                  variant="ghost"
                  {disabled}
                  href={client.getRequestLogDownloadURL(file.name)}
                  download={file.name}
                  aria-label={`Download ${file.name}`}><Download size={14} />Download</Button
                >
              </div>
            </li>{/each}
        </ul>
      {/if}
      <div class="log-footer">
        <span class="muted">{filteredFiles.length} matching / {files.length} saved files</span>
        <div class="actions">
          <Button
            variant="outline"
            disabled={currentFilePage === 0}
            onclick={() => (filePage = currentFilePage - 1)}>Previous files</Button
          ><span>Page {currentFilePage + 1} of {filePageCount}</span><Button
            variant="outline"
            disabled={currentFilePage >= filePageCount - 1}
            onclick={() => (filePage = currentFilePage + 1)}>Next files</Button
          >
        </div>
      </div>
    </Tabs.Content>
  </Tabs.Root>
</section>
<Dialog.Root bind:open={detailOpen}>
  <Dialog.Content
    class="log-detail-dialog w-[calc(100%_-_2rem)] sm:max-w-4xl min-w-0 max-h-[90dvh] overflow-y-auto"
  >
    <Dialog.Header
      ><Dialog.Title class="break-all pr-8">{detailTitle}</Dialog.Title><Dialog.Description
        >{detailFile
          ? `${fileSize(detailOffset)} loaded of ${fileSize(detailSize)}. Preview loads in 64 KiB pages; download retrieves the complete file.`
          : 'Complete application log line. Select and copy text or use the copy button.'}</Dialog.Description
      ></Dialog.Header
    >
    {#if detailError}<div class="error-banner" role="alert">{detailError}</div>{/if}
    {#if detailBusy}<p role="status" class="muted">Loading preview…</p>{/if}
    {#if detailNotice}<p role="status" class="muted">{detailNotice}</p>{/if}
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <pre
      class="detail-text"
      tabindex="0"
      role="region"
      aria-label="Full log message">{detailText}</pre>
    <Dialog.Footer class="flex-wrap gap-2">
      {#if detailFile}<Button
          variant="outline"
          disabled={detailBusy || disabled}
          onclick={() => preview(detailFile)}>Reload preview</Button
        >{#if detailMore}<Button
            variant="outline"
            disabled={detailBusy || disabled}
            onclick={() => preview(detailFile, true)}>Load more</Button
          >{/if}<Button
          variant="outline"
          {disabled}
          href={client.getRequestLogDownloadURL(detailFile)}
          download={detailFile}><Download size={14} />Download complete file</Button
        >{/if}
      <Button variant="outline" disabled={!detailText} onclick={() => copy(detailText)}
        ><Copy size={14} />Copy message</Button
      >
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

<style>
  .logs-panel {
    min-width: 0;
  }
  .logs-panel :global(.log-tabs) {
    height: auto;
    flex-wrap: wrap;
    max-width: calc(100% - 48px);
  }
  .logs-panel :global(.log-tabs [data-slot='tabs-trigger']) {
    height: auto;
    min-height: 36px;
    white-space: normal;
  }
  .logs-panel :global(.log-tabs [data-slot='tabs-trigger']::after) {
    display: none;
  }
  .toolbar,
  .actions,
  .wrap-control {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }
  .toolbar {
    padding: 0 24px 16px;
  }
  .compact {
    padding-bottom: 14px;
  }
  .search {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: 1 1 230px;
    min-width: 0;
  }
  .search :global(svg) {
    flex-shrink: 0;
  }
  .severity {
    display: flex;
    gap: 8px;
    align-items: center;
    flex-wrap: wrap;
  }
  .severity :global([data-slot='select-trigger']) {
    height: auto;
    min-height: 36px;
  }
  .wrap-control {
    font-size: 13px;
    padding-block: 8px;
  }
  form :global(input) {
    flex: 1 1 210px;
    min-width: 0;
  }
  .log-message {
    margin: 0 24px 16px;
    overflow-wrap: anywhere;
  }
  .log-lines {
    max-height: 560px;
    overflow: auto;
    border-block: 1px solid var(--border);
  }
  .log-lines:focus-visible,
  .detail-text:focus-visible {
    outline: 2px solid var(--ring);
    outline-offset: -2px;
  }
  .log-row {
    display: grid;
    grid-template-columns: max-content max-content minmax(0, 1fr);
    gap: 8px;
    text-align: left;
    width: 100%;
    padding: 8px 16px;
    border-bottom: 1px solid var(--border);
    font:
      12px/1.7 ui-monospace,
      SFMono-Regular,
      Menlo,
      monospace;
    color: var(--foreground);
  }
  .log-row:hover {
    background: var(--muted);
  }
  .log-row:focus-visible {
    outline: 2px solid var(--ring);
    outline-offset: -2px;
  }
  .line-number {
    color: var(--muted-foreground);
  }
  .level {
    font-size: 11px;
    text-transform: uppercase;
    color: var(--muted-foreground);
  }
  .level[data-level='error'] {
    color: var(--destructive);
  }
  .level[data-level='warn'] {
    color: var(--warning);
  }
  .line-text {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    min-width: 0;
  }
  .nowrap .line-text {
    white-space: pre;
  }
  .reset {
    background: var(--muted);
  }
  .log-footer {
    display: flex;
    gap: 16px;
    justify-content: space-between;
    flex-wrap: wrap;
    padding: 16px 24px;
    font-size: 12px;
  }
  .file-list {
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .file-list li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    flex-wrap: wrap;
    border-top: 1px solid var(--border);
    padding: 16px 24px;
  }
  .file-info {
    display: flex;
    flex-direction: column;
    gap: 5px;
    min-width: 0;
    flex: 1 1 250px;
    overflow-wrap: anywhere;
  }
  .detail-text {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    max-height: 50dvh;
    overflow: auto;
    min-width: 0;
    padding-block: 16px;
    border-block: 1px solid var(--border);
    font:
      12px/1.7 ui-monospace,
      SFMono-Regular,
      Menlo,
      monospace;
  }
  @media (min-width: 640px) {
    .logs-panel :global(.log-tabs [data-slot='tabs-trigger']) {
      white-space: nowrap;
    }
  }
  @media (max-width: 480px) {
    .toolbar {
      padding-inline: 16px;
    }
    .log-footer,
    .file-list li {
      padding-inline: 16px;
    }
    .log-row {
      grid-template-columns: max-content max-content minmax(0, 1fr);
      padding-inline: 8px;
      gap: 5px;
    }
    .log-message {
      margin-inline: 16px;
    }
  }
</style>
