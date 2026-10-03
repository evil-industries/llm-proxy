<script lang="ts">
  import { formatDateTime, formatTime } from '$lib/datetime';
  import { correlateLogs, logPresentation } from '$lib/logs';
  import { onMount, untrack } from 'svelte';
  import { subscribeChanges, Changes } from '$lib/realtime';
  import {
    Copy,
    Download,
    Pause,
    Play,
    RefreshCw,
    Search,
    ScrollText,
    FileText,
    ChevronRight,
    ChevronLeft,
    Ellipsis,
    RotateCcw
  } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import { Switch } from '$lib/components/ui/switch/index.js';
  import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
  import * as Table from '$lib/components/ui/table/index.js';
  import * as Dialog from '$lib/components/ui/dialog/index.js';
  import * as Tabs from '$lib/components/ui/tabs/index.js';
  import * as Select from '$lib/components/ui/select/index.js';
  import type { ManagementClient, ManagementConfig, RequestLogFile } from '$lib/api';
  import {
    LOG_LEVELS,
    LOG_PAGE_SIZE,
    LOG_FETCH_SIZE,
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
  let thread = $state('');
  let detailThread = $state('');
  const contexts = $derived(correlateLogs(entries));
  const threads = $derived([
    ...new Set(
      [...contexts.values()].flatMap((context) => (context.thread ? [context.thread] : []))
    )
  ]);
  const threadListID = $props.id();
  let wrap = $state(true);
  let page = $state(0);
  let sequence = 0;
  let controller: AbortController;
  let alive = $state(false);
  let queued = false;
  let filesQueued = false;
  const loggingDisabled = $derived(config['logging-to-file'] === false);
  const filtered = $derived(filterLogs(entries, query, severity, thread, contexts));
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
        queued = false;
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
        const additions: LogEntry[] = data.lines.map((text, index) => ({
          id: sequence++,
          text,
          level: logLevel(text),
          timestamp: data.timestamps?.[index] ? data.timestamps[index] * 1000 : undefined
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
              Math.ceil(
                filterLogs(entries, query, severity, thread, contexts).length / LOG_PAGE_SIZE
              ) - 1
            );
        }
        cursor = data['next-cursor'];
        loaded = true;
        catchingUp = more;
        if (!more) lastUpdated = formatTime();
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
      if (alive) {
        busy = false;
        if (queued) {
          queued = false;
          liveUpdate(Changes.logs);
        }
      }
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
      if (alive) {
        filesBusy = false;
        if (filesQueued) {
          filesQueued = false;
          liveUpdate(Changes.requestLogs);
        }
      }
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
    detailThread = contexts.get(entry.id)?.thread ?? '';
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
  function liveUpdate(topics: number) {
    if (!alive || disabled || document.visibilityState === 'hidden') return;
    if (topics & Changes.logs && tab === 'server' && !paused && !loggingDisabled) {
      if (busy) queued = true;
      else void load();
    }
    if (topics & Changes.requestLogs && tab === 'requests') {
      if (filesBusy) filesQueued = true;
      else void loadFiles();
    }
  }
  onMount(() => {
    alive = true;
    controller = new AbortController();
    const unsubscribe = disabled ? () => {} : subscribeChanges(liveUpdate);
    const visible = () => {
      if (document.visibilityState !== 'hidden') liveUpdate(Changes.logs | Changes.requestLogs);
    };
    document.addEventListener('visibilitychange', visible);
    return () => {
      alive = false;
      unsubscribe();
      document.removeEventListener('visibilitychange', visible);
      controller.abort();
      previewController?.abort();
    };
  });
  $effect(() => {
    if (tab === 'server' && !loggingDisabled && alive) untrack(() => liveUpdate(Changes.logs));
  });
  $effect(() => {
    if (tab === 'requests' && !filesLoaded && !filesBusy && !filesError && alive) void loadFiles();
  });
  $effect(() => {
    if (!detailOpen) previewController?.abort();
  });
</script>

<section class="panel logs-panel" aria-label="Log inspection">
  <Tabs.Root bind:value={tab} class="flex-col min-w-0 min-h-0 flex-1">
    <div class="logs-topbar">
      <Tabs.List variant="line" class="log-tabs max-w-full"
        ><Tabs.Trigger value="server">Server logs</Tabs.Trigger><Tabs.Trigger value="requests"
          >Request logs</Tabs.Trigger
        ></Tabs.List
      >
      {#if tab === 'server'}
        <div class="log-controls">
          <Button
            variant="ghost"
            disabled={disabled || loggingDisabled}
            onclick={togglePaused}
            aria-label={paused ? 'Resume live' : 'Pause live'}
            >{#if paused}<Play size={14} />{:else}<Pause size={14} />{/if}<span
              class="control-label">{paused ? 'Resume live' : 'Pause live'}</span
            ></Button
          >
          <Button
            variant="ghost"
            disabled={disabled || busy || loggingDisabled}
            aria-label="Refresh logs"
            onclick={() => load()}
            ><RefreshCw size={14} /><span class="control-label">Refresh logs</span></Button
          >
          <Button
            variant="ghost"
            disabled={disabled || busy || loggingDisabled}
            aria-label="Load latest (replace history)"
            title="Load latest (replace history)"
            onclick={() => load(true)}
            ><RotateCcw size={14} /><span class="control-label">Load latest</span></Button
          >
          <Badge variant="outline" title={lastUpdated ? `Last updated ${lastUpdated}` : undefined}
            >{loggingDisabled
              ? 'Logging disabled'
              : paused
                ? 'Paused'
                : catchingUp
                  ? 'Catching up…'
                  : busy
                    ? 'Loading'
                    : 'Live updates'}</Badge
          >
        </div>
      {/if}
    </div>
    <Tabs.Content value="server" class="min-w-0 min-h-0 flex flex-col">
      <div class="toolbar log-filters">
        <div class="search">
          <Search size={16} /><Input
            aria-label="Search logs"
            placeholder="Search loaded logs…"
            bind:value={query}
            oninput={() => (page = 0)}
          />
        </div>
        <div class="thread-filter">
          <Input
            aria-label="Filter by thread"
            placeholder="Thread ID…"
            list={threadListID}
            bind:value={thread}
            oninput={() => (page = 0)}
          />
          <datalist id={threadListID}
            >{#each threads as id}<option value={id}></option>{/each}</datalist
          >
        </div>
        <div class="severity">
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
              ><Select.Group
                ><Select.Item value="all" label="All levels">All levels</Select.Item
                >{#each LOG_LEVELS as level}<Select.Item value={level} label={level}
                    >{level}</Select.Item
                  >{/each}</Select.Group
              ></Select.Content
            >
          </Select.Root>
        </div>
        <div class="filter-options">
          <Button
            variant="ghost"
            disabled={!query && !thread && severity === 'all'}
            onclick={() => {
              query = '';
              thread = '';
              severity = 'all';
              page = 0;
            }}>Clear filters</Button
          >
          <label class="wrap-control"
            ><Switch aria-label="Wrap log lines" bind:checked={wrap} />Wrap lines</label
          >
        </div>
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
          <h3>
            {query || thread || severity !== 'all' ? 'No matching log lines' : 'No logs available'}
          </h3>
          <p>
            {query || thread || severity !== 'all'
              ? 'Try another search or clear the filters.'
              : 'New application activity will appear when the server writes it.'}
          </p>
        </div>
      {:else}
        <Table.Root
          class={wrap ? 'min-w-[760px] table-fixed' : 'min-w-[760px] table-auto'}
          aria-label="Server logs"
          containerProps={{
            class: 'log-lines min-h-0 flex-1 overflow-auto',
            role: 'region',
            'aria-label': 'Log output',
            tabindex: 0,
            'aria-busy': busy
          }}
        >
          <Table.Header sticky>
            <Table.Row>
              <Table.Head class="w-44">Time</Table.Head>
              <Table.Head class="w-20">Level</Table.Head>
              <Table.Head>Message</Table.Head>
              <Table.Head class="w-44">Thread / account</Table.Head>
              <Table.Head class="w-12"><span class="sr-only">Details</span></Table.Head>
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {#each visible as entry (entry.id)}
              {@const presentation = logPresentation(entry.text, entry.timestamp)}
              {@const context = contexts.get(entry.id)}
              <Table.Row class="log-row" data-state={entry.reset ? 'selected' : undefined}>
                <Table.Cell class="align-top"
                  ><span class="log-time">{presentation.timestamp}</span></Table.Cell
                >
                <Table.Cell class="align-top"
                  ><span class="level" data-level={entry.level}>{entry.level}</span></Table.Cell
                >
                <Table.Cell class="align-top">
                  <div class="line-text" class:nowrap={!wrap}>{presentation.message}</div>
                  {#if context?.request}<div class="log-context">
                      Request {context.request}
                    </div>{/if}
                </Table.Cell>
                <Table.Cell class="align-top">
                  <div class="thread-context">
                    <span>{context?.thread ?? '—'}</span>
                    {#if context?.account}<span class="muted">{context.account}</span>{/if}
                  </div>
                </Table.Cell>
                <Table.Cell class="align-top">
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    onclick={() => inspect(entry)}
                    aria-label={`Inspect log line ${entry.id + 1}`}><ChevronRight /></Button
                  >
                </Table.Cell>
              </Table.Row>
            {/each}
          </Table.Body>
        </Table.Root>
      {/if}
      {#if entries.length}
        <div class="log-footer">
          <span class="muted"
            >{filtered.length === entries.length
              ? `${entries.length.toLocaleString('de-DE')} ${entries.length === 1 ? 'line' : 'lines'}`
              : `${filtered.length.toLocaleString('de-DE')} of ${entries.length.toLocaleString('de-DE')} lines`}</span
          >
          <div class="footer-actions">
            {#if pageCount > 1}
              <span class="muted">Page {currentPage + 1} of {pageCount}</span>
              <Button
                variant="ghost"
                size="icon-sm"
                disabled={currentPage === 0}
                onclick={() => (page = currentPage - 1)}
                aria-label="Previous lines"><ChevronLeft /></Button
              >
              <Button
                variant="ghost"
                size="icon-sm"
                disabled={currentPage >= pageCount - 1}
                onclick={() => (page = currentPage + 1)}
                aria-label="Next lines"><ChevronRight /></Button
              >
            {/if}
            {#if visible.length}
              <DropdownMenu.Root>
                <DropdownMenu.Trigger>
                  {#snippet child({ props })}<Button
                      {...props}
                      variant="ghost"
                      size="icon-sm"
                      aria-label="Log actions"><Ellipsis /></Button
                    >{/snippet}
                </DropdownMenu.Trigger>
                <DropdownMenu.Content align="end" side="top" class="w-44">
                  <DropdownMenu.Group>
                    <DropdownMenu.Item
                      onSelect={() => copy(visible.map((entry) => entry.text).join('\n'))}
                      ><Copy />Copy visible</DropdownMenu.Item
                    >
                    <DropdownMenu.Item onSelect={downloadVisible}
                      ><Download />Download visible</DropdownMenu.Item
                    >
                  </DropdownMenu.Group>
                </DropdownMenu.Content>
              </DropdownMenu.Root>
            {/if}
          </div>
        </div>
      {/if}
    </Tabs.Content>
    <Tabs.Content value="requests" class="min-w-0 min-h-0 flex flex-col">
      <div class="toolbar">
        <div class="search">
          <Search size={16} /><Input
            aria-label="Search request log files"
            placeholder="Search file names…"
            bind:value={fileQuery}
            oninput={() => (filePage = 0)}
          />
        </div>
        <Button variant="ghost" disabled={disabled || filesBusy} onclick={loadFiles}
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
        <Table.Root
          class="min-w-[740px] table-fixed"
          aria-label="Saved request log files"
          containerProps={{
            class: 'min-h-0 flex-1 overflow-auto',
            role: 'region',
            'aria-label': 'Request log files',
            tabindex: 0
          }}
        >
          <Table.Header sticky
            ><Table.Row>
              <Table.Head>File</Table.Head><Table.Head class="w-20">Kind</Table.Head>
              <Table.Head class="w-24">Size</Table.Head><Table.Head class="w-44"
                >Modified</Table.Head
              >
              <Table.Head class="w-36"><span class="sr-only">Actions</span></Table.Head>
            </Table.Row></Table.Header
          >
          <Table.Body>
            {#each filteredFiles.slice(currentFilePage * 50, (currentFilePage + 1) * 50) as file (file.name)}
              <Table.Row>
                <Table.Cell><span class="file-name">{file.name}</span></Table.Cell>
                <Table.Cell>{file.kind}</Table.Cell>
                <Table.Cell>{fileSize(file.size)}</Table.Cell>
                <Table.Cell
                  ><span class="log-time">{formatDateTime(file.modified * 1000)}</span></Table.Cell
                >
                <Table.Cell
                  ><div class="file-actions">
                    <Button
                      variant="ghost"
                      size="sm"
                      {disabled}
                      onclick={() => preview(file.name)}
                      aria-label={`Preview ${file.name}`}>Preview</Button
                    >
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      {disabled}
                      href={client.getRequestLogDownloadURL(file.name)}
                      download={file.name}
                      aria-label={`Download ${file.name}`}><Download /></Button
                    >
                  </div></Table.Cell
                >
              </Table.Row>
            {/each}
          </Table.Body>
        </Table.Root>
      {/if}
      {#if files.length}
        <div class="log-footer">
          <span class="muted"
            >{filteredFiles.length === files.length
              ? `${files.length.toLocaleString('de-DE')} ${files.length === 1 ? 'file' : 'files'}`
              : `${filteredFiles.length.toLocaleString('de-DE')} of ${files.length.toLocaleString('de-DE')} files`}</span
          >
          {#if filePageCount > 1}<div class="footer-actions">
              <span class="muted">Page {currentFilePage + 1} of {filePageCount}</span>
              <Button
                variant="ghost"
                size="icon-sm"
                disabled={currentFilePage === 0}
                onclick={() => (filePage = currentFilePage - 1)}
                aria-label="Previous files"><ChevronLeft /></Button
              >
              <Button
                variant="ghost"
                size="icon-sm"
                disabled={currentFilePage >= filePageCount - 1}
                onclick={() => (filePage = currentFilePage + 1)}
                aria-label="Next files"><ChevronRight /></Button
              >
            </div>{/if}
        </div>
      {/if}
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
      {#if !detailFile && detailThread}<Button
          variant="secondary"
          onclick={() => {
            thread = detailThread;
            page = 0;
            detailOpen = false;
          }}>Show this thread</Button
        >{/if}
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
    min-height: 0;
    height: var(--logs-height, 100dvh);
    display: flex;
    flex-direction: column;
    overflow: auto;
  }
  .logs-panel :global([data-slot='tabs']),
  .logs-panel :global([data-slot='tabs-content']) {
    overflow: auto;
  }
  .logs-panel :global([data-slot='table-container']) {
    min-height: 128px;
  }
  .logs-panel :global(.log-tabs) {
    height: auto;
    flex-wrap: wrap;
    max-width: 100%;
  }
  .logs-panel :global(.log-tabs [data-slot='tabs-trigger']) {
    height: auto;
    min-height: 36px;
    white-space: normal;
  }
  .logs-panel :global(.log-tabs [data-slot='tabs-trigger']::after) {
    bottom: 0;
  }
  .toolbar,
  .wrap-control {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }
  .logs-panel :global([data-slot='tabs-content'] > :not([data-slot='table-container'])) {
    flex-shrink: 0;
  }
  .logs-topbar {
    flex-shrink: 0;
  }
  .toolbar {
    padding: 16px 0;
    justify-content: flex-start;
  }
  .logs-topbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
    padding: 16px 0;
    border-bottom: 1px solid var(--border);
  }
  .log-controls {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
  }
  .log-filters {
    display: grid;
    grid-template-columns: minmax(12rem, 2fr) minmax(10rem, 1fr) auto auto;
    gap: 12px;
  }
  .filter-options {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: flex-end;
    gap: 12px;
  }
  .log-time {
    font-size: 12px;
    color: var(--muted-foreground);
    font-variant-numeric: tabular-nums;
    overflow-wrap: anywhere;
  }
  @media (max-width: 1100px) {
    .filter-options {
      grid-column: 1 / -1;
    }
    .log-filters {
      grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto;
    }
  }
  @media (max-width: 700px) {
    .control-label {
      display: none;
    }
    .filter-options {
      justify-content: flex-start;
    }
    .log-filters {
      grid-template-columns: minmax(0, 1fr) auto;
    }
    .log-filters > .search {
      grid-column: 1 / -1;
    }
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
    margin: 0 0 12px;
    overflow-wrap: anywhere;
  }
  .logs-panel :global([data-slot='table-container']):focus-visible,
  .detail-text:focus-visible {
    outline: 2px solid var(--ring);
    outline-offset: -2px;
  }
  .level {
    font-size: 0.6875rem;
    text-transform: uppercase;
    color: var(--muted-foreground);
  }
  .level[data-level='error'] {
    color: var(--destructive);
  }
  .level[data-level='info'] {
    color: var(--info);
  }
  .level[data-level='warn'] {
    color: var(--warning);
  }
  .thread-filter {
    flex: 1 1 15rem;
    min-width: 0;
  }
  .log-context {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 16px;
    white-space: normal;
    color: var(--muted-foreground);
    font-size: 0.75rem;
    overflow-wrap: anywhere;
    margin-top: 0.25rem;
  }
  .line-text {
    font:
      13px/1.6 ui-monospace,
      monospace;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    min-width: 0;
  }
  .line-text.nowrap {
    white-space: pre;
  }
  .footer-actions {
    display: flex;
    align-items: center;
    gap: 4px;
    margin-left: auto;
  }
  .log-footer {
    display: flex;
    align-items: center;
    gap: 8px 16px;
    justify-content: space-between;
    flex-wrap: wrap;
    padding: 12px 0;
    font-size: 12px;
  }
  .thread-context {
    display: grid;
    gap: 4px;
    white-space: normal;
    overflow-wrap: anywhere;
    font-family: ui-monospace, monospace;
    font-size: 12px;
  }
  .file-name {
    display: block;
    white-space: normal;
    overflow-wrap: anywhere;
  }
  .file-actions {
    display: flex;
    align-items: center;
    gap: 4px;
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
      0.8125rem/1.65 ui-monospace,
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
    .toolbar,
    .logs-topbar {
      padding-inline: 0;
    }
    .log-footer {
      padding-inline: 0;
    }
    .log-message {
      margin-inline: 16px;
    }
  }
</style>
