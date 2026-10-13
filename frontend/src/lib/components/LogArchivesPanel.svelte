<script lang="ts">
  import { onMount } from 'svelte';
  import { Download, RefreshCw } from '@lucide/svelte';
  import type { LogArchiveFile, ManagementClient } from '$lib/api';
  import { formatDateTime } from '$lib/datetime';
  import { fileSize } from '$lib/logs';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import * as Table from '$lib/components/ui/table/index.js';

  let { client, disabled = false }: { client: ManagementClient; disabled?: boolean } = $props();
  let files = $state<LogArchiveFile[]>([]);
  let cursor = $state('');
  let loaded = $state(false);
  let busy = $state(false);
  let error = $state('');
  let controller: AbortController;

  async function load(more = false) {
    if (busy || disabled) return;
    busy = true;
    error = '';
    try {
      const result = await client.listLogArchives(
        { limit: 100, ...(more ? { cursor } : {}) },
        controller.signal
      );
      if (controller.signal.aborted) return;
      if (more && result.next_cursor && result.next_cursor === cursor)
        throw new Error('The archive cursor did not advance. Refresh the list to try again.');
      // A file can finish uploading between pages. Deduplicate repeat entries without
      // hiding an independently downloadable local or S3 copy.
      const combined = more ? [...files, ...result.files] : result.files;
      files = [...new Map(combined.map((file) => [`${file.source}:${file.key}`, file])).values()];
      cursor = result.next_cursor ?? '';
      loaded = true;
    } catch (cause) {
      if (!controller.signal.aborted)
        error = cause instanceof Error ? cause.message : 'Unable to load log archives.';
    } finally {
      if (!controller.signal.aborted) busy = false;
    }
  }
  onMount(() => {
    controller = new AbortController();
    void load();
    return () => controller.abort();
  });
</script>

<div class="min-w-0 min-h-0 flex flex-col flex-1">
  <div class="flex items-center justify-between gap-4 p-4">
    <p class="muted text-sm">
      Download application, request, and error logs from local storage or S3. Compressed archives
      download as gzip files.
    </p>
    <Button
      variant="outline"
      disabled={disabled || busy}
      onclick={() => load()}
      aria-label="Refresh archives"><RefreshCw size={14} />Refresh</Button
    >
  </div>
  {#if error}<div role="alert" class="error-banner m-4">{error}</div>{/if}
  {#if busy && !loaded}
    <p role="status" class="empty-state">Loading log archives…</p>
  {:else if loaded && !files.length}
    <div class="empty-state">
      <Download size={26} />
      <h3>No log archives</h3>
      <p>
        {cursor
          ? 'More files may be available on the next page.'
          : 'Completed logs will appear here when available.'}
      </p>
    </div>
  {:else if files.length}
    <Table.Root
      class="min-w-[700px] table-fixed"
      aria-label="Downloadable log archives"
      containerProps={{
        class: 'min-h-0 flex-1 overflow-auto',
        role: 'region',
        'aria-label': 'Log archives',
        tabindex: 0
      }}
    >
      <Table.Header sticky
        ><Table.Row>
          <Table.Head>File</Table.Head><Table.Head class="w-24">Source</Table.Head><Table.Head
            class="w-24">Size</Table.Head
          ><Table.Head class="w-44">Modified</Table.Head><Table.Head class="w-28"
            ><span class="sr-only">Download</span></Table.Head
          >
        </Table.Row></Table.Header
      >
      <Table.Body>
        {#each files as file (`${file.source}:${file.key}`)}
          <Table.Row>
            <Table.Cell><span class="block truncate" title={file.key}>{file.name}</span></Table.Cell
            >
            <Table.Cell
              ><Badge variant="outline">{file.source === 's3' ? 'S3' : 'Local'}</Badge></Table.Cell
            >
            <Table.Cell>{fileSize(file.size)}</Table.Cell>
            <Table.Cell>{formatDateTime(file.modified * 1000)}</Table.Cell>
            <Table.Cell
              ><Button
                variant="ghost"
                size="sm"
                {disabled}
                href={client.getLogArchiveDownloadURL(file)}
                download={file.name}
                aria-label={`Download ${file.name} from ${file.source === 's3' ? 'S3' : 'local storage'}`}
                ><Download size={14} />Download</Button
              ></Table.Cell
            >
          </Table.Row>
        {/each}
      </Table.Body>
    </Table.Root>
  {/if}
  {#if loaded}
    <div class="flex items-center justify-between gap-4 p-4">
      <span class="muted text-sm">{files.length.toLocaleString('de-DE')} files loaded</span>
      {#if cursor}<Button variant="outline" disabled={disabled || busy} onclick={() => load(true)}
          >{busy ? 'Loading…' : 'Load more archives'}</Button
        >{/if}
    </div>
  {/if}
</div>
