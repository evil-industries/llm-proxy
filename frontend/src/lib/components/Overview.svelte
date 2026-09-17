<script lang="ts">
  import { ArrowUpRight, KeyRound, Activity } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import RequestChart from './RequestChart.svelte';
  import type { AuthFile } from '$lib/api';
  let {
    files = [],
    keyCount = 0,
    strategy = 'round-robin',
    oncredentials = () => {}
  }: {
    files?: AuthFile[];
    keyCount?: number;
    strategy?: string;
    oncredentials?: () => void;
  } = $props();
  let requests = $derived(
    files.reduce((sum, file) => sum + (file.success ?? 0) + (file.failed ?? 0), 0)
  );
  let failures = $derived(files.reduce((sum, file) => sum + (file.failed ?? 0), 0));
  let active = $derived(files.filter((file) => !file.disabled).length);
  let providers = $derived(
    [...new Set(files.map((file) => file.provider || file.type || 'Unknown'))].map((name) => ({
      name,
      files: files.filter((file) => (file.provider || file.type || 'Unknown') === name)
    }))
  );
  const number = (value: number) => new Intl.NumberFormat('en-US').format(value);
</script>

<div class="overview">
  <div class="metrics">
    <div class="panel metric">
      <p class="muted">Credential requests</p>
      <strong>{number(requests)}</strong>
      <p class="metric-note muted">Recorded by connected credentials</p>
    </div>
    <div class="panel metric">
      <p class="muted">Success rate</p>
      <strong>{requests ? `${((1 - failures / requests) * 100).toFixed(1)}%` : '—'}</strong>
      <p class="metric-note muted">{number(failures)} failed requests</p>
    </div>
    <div class="panel metric">
      <p class="muted">Enabled credentials</p>
      <strong>{active}<span class="muted"> / {files.length}</span></strong>
      <p class="metric-note muted">Across {providers.length} providers</p>
    </div>
    <div class="panel metric">
      <p class="muted">Client API keys</p>
      <strong>{keyCount}</strong>
      <p class="metric-note muted">Keys authorized to use this proxy</p>
    </div>
  </div>
  <RequestChart {files} />
  <section class="panel">
    <div class="panel-header">
      <div class="section-heading">
        <h2>Connected providers</h2>
        <p class="muted">Credential availability across your instance.</p>
      </div>
      <Button variant="outline" onclick={oncredentials}
        >Manage credentials <ArrowUpRight size={14} /></Button
      >
    </div>
    {#if providers.length}
      <div class="provider-list">
        {#each providers as provider}<div class="provider-row">
            <div class="provider-icon">{provider.name.slice(0, 1).toUpperCase()}</div>
            <div class="provider-name">
              <h3>{provider.name}</h3>
              <p class="muted">
                {provider.files.length}
                {provider.files.length === 1 ? 'credential' : 'credentials'}
              </p>
            </div>
            <span class:offline={!provider.files.some((file) => !file.disabled)} class="status"
              >{provider.files.filter((file) => !file.disabled).length} enabled</span
            ><span class="provider-requests mono"
              >{number(
                provider.files.reduce(
                  (sum, file) => sum + (file.success ?? 0) + (file.failed ?? 0),
                  0
                )
              )}<span class="muted"> requests</span></span
            >
          </div>{/each}
      </div>
    {:else}<div class="empty-state">
        <KeyRound size={25} />
        <h3>No credentials connected</h3>
        <p>Upload a credential file to start routing requests.</p>
        <Button variant="outline" onclick={oncredentials}>Manage credentials</Button>
      </div>{/if}
  </section>
  <div class="overview-footer">
    <Activity size={15} /><span
      >Routing strategy <strong
        >{strategy === 'fill-first'
          ? 'Fill first'
          : strategy === 'weighted-round-robin'
            ? 'Weighted round robin'
            : strategy === 'round-robin'
              ? 'Round robin'
              : strategy}</strong
      ></span
    ><span class="muted">Counters reflect the server’s available history.</span>
  </div>
</div>

<style>
  .overview {
    display: grid;
    gap: 24px;
  }
  .metrics {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr));
    gap: 16px;
  }
  .metric {
    overflow-wrap: anywhere;
    padding: 21px 23px;
    display: grid;
    gap: 14px;
  }
  .metric strong {
    font-size: 30px;
    line-height: 1.2;
    font-weight: 550;
    letter-spacing: -1px;
  }
  .metric strong span {
    margin-left: 0.2em;
    font-size: 20px;
    font-weight: 400;
  }
  .metric-note {
    font-size: 12px;
  }
  .provider-row {
    display: flex;
    align-items: center;
    gap: 15px;
    flex-wrap: wrap;
    padding: 22px 24px;
    border-bottom: 1px solid #ededed;
  }
  .provider-row:last-child {
    border: 0;
  }
  .provider-icon {
    width: 38px;
    min-height: 38px;
    padding: 7px;
    display: grid;
    place-items: center;
    border: 1px solid #e5e5e5;
    border-radius: 8px;
    font-weight: 600;
    flex-shrink: 0;
  }
  .provider-name {
    flex: 1;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .provider-name h3 {
    text-transform: capitalize;
  }
  .provider-name p {
    font-size: 12px;
    margin-top: 3px;
  }
  .provider-requests {
    flex: 0 1 175px;
    min-width: 0;
    overflow-wrap: anywhere;
    text-align: right;
    font-size: 12px;
  }
  .provider-requests .muted {
    margin-left: 0.3em;
  }
  .overview-footer {
    display: flex;
    gap: 10px;
    flex-wrap: wrap;
    align-items: center;
    color: #666;
    font-size: 12px;
  }
  .overview-footer strong {
    color: #171717;
    font-weight: 500;
    margin-left: 8px;
  }
  .overview-footer > span:last-child {
    margin-left: auto;
  }

  @media (max-width: 640px) {
    .metric {
      padding: 18px;
    }
    .metric strong {
      font-size: 26px;
    }
    .provider-row {
      padding: 18px;
      flex-wrap: wrap;
      gap: 12px;
    }
    .provider-requests {
      width: auto;
      margin-left: 50px;
    }
    .overview-footer > span:last-child {
      margin-left: 0;
      width: 100%;
    }
  }
  @media (max-width: 360px) {
    .metrics {
      grid-template-columns: 1fr;
    }
  }
</style>
