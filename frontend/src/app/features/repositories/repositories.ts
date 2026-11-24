import { DatePipe } from '@angular/common';
import { HttpClient } from '@angular/common/http';
import { Component, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { Subject, debounceTime, distinctUntilChanged, firstValueFrom } from 'rxjs';
import { PagedResult, Repository } from '../../core/models';
import { SyncEventsService } from '../../core/events/sync-events.service';
import { getApiErrorMessage } from '../../core/http/api-error';
import { ProviderIconComponent } from '../../shared/provider-icon/provider-icon.component';
import { StatusBadgeComponent } from '../../shared/status-badge/status-badge.component';
import { AlertBannerComponent } from '../../shared/alert-banner/alert-banner.component';

@Component({
  selector: 'app-repositories',
  standalone: true,
  imports: [DatePipe, FormsModule, RouterLink, ProviderIconComponent, StatusBadgeComponent, AlertBannerComponent],
  template: `
    <div class="space-y-6">
      <!-- Header -->
      <div class="flex items-center justify-between gap-4 flex-wrap">
        <div>
          <h2 class="text-2xl font-semibold text-white">Repositories</h2>
          <p class="text-sm text-slate-400">Pull and push mirrors managed by this service.</p>
        </div>
        <div class="flex items-center gap-2">
        <button type="button" (click)="copyWebhookUrl()"
          title="Copy the webhook URL to configure in GitHub/GitLab"
          class="flex items-center gap-2 rounded-xl border border-slate-700 px-4 py-2 text-sm font-medium text-slate-300 hover:border-slate-500 hover:text-white transition-colors">
          <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" viewBox="0 0 20 20" fill="currentColor">
            <path d="M8 2a1 1 0 000 2h2a1 1 0 100-2H8z"/><path d="M3 5a2 2 0 012-2 3 3 0 003 3h2a3 3 0 003-3 2 2 0 012 2v6h-4.586l1.293-1.293a1 1 0 00-1.414-1.414l-3 3a1 1 0 000 1.414l3 3a1 1 0 001.414-1.414L10.414 13H15v3a2 2 0 01-2 2H5a2 2 0 01-2-2V5zM15 11h2a1 1 0 110 2h-2v-2z"/>
          </svg>
          Copy webhook URL
        </button>
        <a routerLink="/repositories/new"
          class="flex items-center gap-2 rounded-xl bg-emerald-500 px-4 py-2 text-sm font-medium text-slate-950 hover:bg-emerald-400 transition-colors">
          <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" viewBox="0 0 20 20" fill="currentColor">
            <path fill-rule="evenodd" d="M10 3a1 1 0 011 1v5h5a1 1 0 110 2h-5v5a1 1 0 11-2 0v-5H4a1 1 0 110-2h5V4a1 1 0 011-1z" clip-rule="evenodd"/>
          </svg>
          Add repository
        </a>
        </div>
      </div>

      <!-- Active Sync Progress Bar -->
      @if (hasActiveSyncs()) {
        <div class="rounded-xl border border-emerald-500/30 bg-emerald-950/40 p-3.5 space-y-2">
          <div class="flex items-center justify-between text-xs text-emerald-300">
            <span class="flex items-center gap-2 font-medium">
              <svg class="h-4 w-4 animate-spin text-emerald-400" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
                <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z"></path>
              </svg>
              Synchronizing repository in background&hellip;
            </span>
            <span class="inline-flex items-center gap-1 rounded-full bg-emerald-500/20 px-2 py-0.5 text-[11px] font-medium text-emerald-300">
              <span class="h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse"></span>
              LIVE
            </span>
          </div>
          <div class="relative h-1.5 w-full overflow-hidden rounded-full bg-slate-800">
            <div class="absolute inset-y-0 h-full rounded-full bg-emerald-400 animate-indeterminate"></div>
          </div>
        </div>
      }

      <!-- Alerts -->
      <app-alert-banner [message]="successMsg()" type="success" />
      <app-alert-banner [message]="errorMsg()" type="error" />
      <app-alert-banner [message]="webhookHint()" type="info" [dismissible]="true" (dismiss)="webhookHint.set('')" />

      <!-- Search bar -->
      <div class="relative">
        <svg xmlns="http://www.w3.org/2000/svg" class="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-slate-500 pointer-events-none" viewBox="0 0 20 20" fill="currentColor">
          <path fill-rule="evenodd" d="M8 4a4 4 0 100 8 4 4 0 000-8zM2 8a6 6 0 1110.89 3.476l4.817 4.817a1 1 0 01-1.414 1.414l-4.816-4.816A6 6 0 012 8z" clip-rule="evenodd"/>
        </svg>
        <input [(ngModel)]="searchQuery" name="search" (ngModelChange)="onSearch()"
          class="w-full rounded-xl border border-slate-700 bg-slate-900 pl-9 pr-4 py-2 text-sm text-white placeholder-slate-500 focus:border-emerald-400 focus:outline-none"
          placeholder="Search repositories…" />
      </div>

      <!-- Table -->
      @if (loading()) {
        <div class="rounded-2xl border border-slate-800 bg-slate-900 p-6 text-slate-300">Loading repositories…</div>
      } @else {
        <div class="overflow-hidden rounded-2xl border border-slate-800 bg-slate-900">
          <table class="min-w-full text-left text-sm text-slate-300">
            <thead class="bg-slate-950/60 text-xs uppercase tracking-wide text-slate-400">
              <tr>
                <th class="px-4 py-3">Name</th>
                <th class="px-4 py-3">Last sync</th>
                <th class="px-4 py-3">Provider</th>
                <th class="px-4 py-3">Status</th>
                <th class="px-4 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              @for (repo of repos(); track repo.id) {
                <tr class="border-t border-slate-800 hover:bg-slate-800/30 transition-colors">
                  <td class="px-4 py-3">
                    <div class="font-medium text-white">{{ repo.name }}</div>
                    <div class="text-xs text-slate-500 mt-0.5 truncate max-w-[200px] md:max-w-xs lg:max-w-sm" [title]="repo.pull_url_ssh + ' → ' + repo.push_url_ssh">
                      {{ repo.pull_url_ssh }} &rarr; {{ repo.push_url_ssh }}
                    </div>
                    @if (isSyncing(repo.id, repo.name)) {
                      <div class="mt-2 relative h-1 w-full max-w-[200px] overflow-hidden rounded-full bg-slate-800">
                        <div class="absolute inset-y-0 h-full rounded-full bg-emerald-400 animate-indeterminate"></div>
                      </div>
                    }
                  </td>
                  <td class="px-4 py-3">
                    @if (repo.last_sync_at) {
                      <span class="text-sm text-slate-300">{{ repo.last_sync_at | date:'short' }}</span>
                    } @else {
                      <span class="text-sm text-slate-500">&mdash;</span>
                    }
                  </td>
                  <td class="px-4 py-3">
                    <div class="flex items-center gap-1.5">
                      <app-provider-icon [provider]="repo.provider_hint || 'generic'" size="sm" />
                      <span class="text-xs capitalize">{{ repo.provider_hint || 'generic' }}</span>
                    </div>
                  </td>
                  <td class="px-4 py-3">
                    <app-status-badge [status]="isSyncing(repo.id, repo.name) ? 'running' : (repo.last_sync_status || (repo.enabled ? 'live' : 'disabled'))" />
                  </td>
                  <td class="px-4 py-3">
                    <div class="flex items-center justify-end gap-2">
                      <!-- Sync now -->
                      <button (click)="syncNow(repo)" [disabled]="isSyncing(repo.id, repo.name)"
                        [title]="isSyncing(repo.id, repo.name) ? 'Sync in progress' : 'Sync now'"
                        class="rounded-lg border border-slate-700 p-1.5 text-slate-400 hover:border-emerald-500 hover:text-emerald-400 transition-colors disabled:opacity-40">
                        <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" [class.animate-spin]="isSyncing(repo.id, repo.name)" viewBox="0 0 20 20" fill="currentColor">
                          <path fill-rule="evenodd" d="M4 2a1 1 0 011 1v2.101a7.002 7.002 0 0111.601 2.566 1 1 0 11-1.885.666A5.002 5.002 0 005.999 7H9a1 1 0 010 2H4a1 1 0 01-1-1V3a1 1 0 011-1zm.008 9.057a1 1 0 011.276.61A5.002 5.002 0 0014.001 13H11a1 1 0 110-2h5a1 1 0 011 1v5a1 1 0 11-2 0v-2.101a7.002 7.002 0 01-11.601-2.566 1 1 0 01.61-1.276z" clip-rule="evenodd"/>
                        </svg>
                      </button>
                      <!-- Edit -->
                      <a [routerLink]="['/repositories', repo.id, 'edit']"
                        title="Edit"
                        class="rounded-lg border border-slate-700 p-1.5 text-slate-400 hover:border-slate-500 hover:text-white transition-colors">
                        <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" viewBox="0 0 20 20" fill="currentColor">
                          <path d="M13.586 3.586a2 2 0 112.828 2.828l-.793.793-2.828-2.828.793-.793zM11.379 5.793L3 14.172V17h2.828l8.38-8.379-2.83-2.828z"/>
                        </svg>
                      </a>
                      <!-- Delete -->
                      <button (click)="deleteRepo(repo)" [disabled]="deletingId() === repo.id"
                        title="Delete"
                        class="rounded-lg border border-slate-700 p-1.5 text-slate-400 hover:border-rose-500 hover:text-rose-400 transition-colors disabled:opacity-40">
                        <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" viewBox="0 0 20 20" fill="currentColor">
                          <path fill-rule="evenodd" d="M9 2a1 1 0 00-.894.553L7.382 4H4a1 1 0 000 2v10a2 2 0 002 2h8a2 2 0 002-2V6a1 1 0 100-2h-3.382l-.724-1.447A1 1 0 0011 2H9zM7 8a1 1 0 012 0v6a1 1 0 11-2 0V8zm5-1a1 1 0 00-1 1v6a1 1 0 102 0V8a1 1 0 00-1-1z" clip-rule="evenodd"/>
                        </svg>
                      </button>
                    </div>
                  </td>
                </tr>
              } @empty {
                <tr>
                  <td colspan="5" class="px-4 py-10 text-center text-slate-400">
                    @if (searchQuery) {
                      No repositories match "{{ searchQuery }}".
                    } @else {
                      No repositories configured yet.
                      <a routerLink="/repositories/new" class="ml-1 text-emerald-400 hover:underline">Add one.</a>
                    }
                  </td>
                </tr>
              }
            </tbody>
          </table>
        </div>

        <!-- Pagination -->
        @if (total() > 0) {
          <div class="flex items-center justify-between text-sm text-slate-400 mt-4 px-2">
            <span>Showing {{ rangeStart() }}&ndash;{{ rangeEnd() }} of {{ total() }} repositories</span>
            <div class="flex items-center gap-2">
              <button (click)="prevPage()" [disabled]="page() === 1"
                class="rounded-lg border border-slate-700 px-3 py-1.5 hover:border-slate-500 hover:text-white transition-colors disabled:opacity-40">
                Previous
              </button>
              <span class="text-slate-500">{{ page() }} / {{ totalPages() }}</span>
              <button (click)="nextPage()" [disabled]="page() >= totalPages()"
                class="rounded-lg border border-slate-700 px-3 py-1.5 hover:border-slate-500 hover:text-white transition-colors disabled:opacity-40">
                Next
              </button>
            </div>
          </div>
        }
      }
    </div>
  `,
})
export class RepositoriesComponent {
  private readonly http = inject(HttpClient);
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);
  readonly syncEvents = inject(SyncEventsService);

  readonly repos = signal<Repository[]>([]);
  readonly loading = signal(true);
  readonly total = signal(0);
  readonly page = signal(1);
  readonly totalPages = signal(1);
  readonly deletingId = signal<number | null>(null);
  readonly successMsg = signal('');
  readonly errorMsg = signal('');
  readonly webhookHint = signal('');
  private webhookHintTimer: ReturnType<typeof setTimeout> | null = null;

  readonly pageSize = 20;
  searchQuery = '';

  readonly rangeStart = computed(() => {
    if (this.total() === 0) return 0;
    return (this.page() - 1) * this.pageSize + 1;
  });
  readonly rangeEnd = computed(() => Math.min(this.page() * this.pageSize, this.total()));

  private readonly searchSubject = new Subject<string>();

  constructor() {
    this.searchSubject.pipe(
      takeUntilDestroyed(),
      debounceTime(300),
      distinctUntilChanged()
    ).subscribe(q => {
      this.router.navigate([], { 
        queryParams: { q: q || null, page: 1 }, 
        queryParamsHandling: 'merge', 
        replaceUrl: true 
      });
    });

    this.route.queryParams.pipe(takeUntilDestroyed()).subscribe(params => {
      this.searchQuery = params['q'] || '';
      const p = parseInt(params['page'], 10);
      this.page.set(isNaN(p) ? 1 : p);
      this.loadRepositories(true);
    });

    // Listen for real-time SSE events
    this.syncEvents.events$.pipe(takeUntilDestroyed()).subscribe(event => {
      const isFinished =
        event.status === 'success' ||
        event.status === 'error' ||
        event.status === 'conflict' ||
        event.status === 'skipped' ||
        event.type === 'sync_completed' ||
        event.type === 'sync_failed';

      if (isFinished) {
        if (event.status === 'success' || event.type === 'sync_completed') {
          this.successMsg.set('Sync completed for ' + (event.repo_name || 'repository') + '.');
        } else {
          this.errorMsg.set(
            'Sync failed for ' +
              (event.repo_name || 'repository') +
              (event.detail ? ': ' + event.detail : '')
          );
        }
        // Refresh repositories table to reflect latest status and timestamps
        void this.loadRepositories(false);
      } else if (event.status === 'running' || event.type === 'sync_started') {
        // Update status badge immediately for the running repo
        this.repos.update(items =>
          items.map(r =>
            (event.repo_id && r.id === event.repo_id) || (event.repo_name && r.name === event.repo_name)
              ? { ...r, last_sync_status: 'running' }
              : r
          )
        );
      }
    });
  }

  isSyncing(repoId: number, repoName?: string): boolean {
    return this.syncEvents.isSyncing(repoId, repoName);
  }

  hasActiveSyncs(): boolean {
    return this.syncEvents.hasActiveSyncs();
  }

  onSearch(): void {
    this.searchSubject.next(this.searchQuery);
  }

  prevPage(): void {
    if (this.page() > 1) {
      this.router.navigate([], { queryParams: { page: this.page() - 1 }, queryParamsHandling: 'merge' });
    }
  }

  nextPage(): void {
    if (this.page() < this.totalPages()) {
      this.router.navigate([], { queryParams: { page: this.page() + 1 }, queryParamsHandling: 'merge' });
    }
  }

  async copyWebhookUrl(): Promise<void> {
    let url = window.location.origin + '/webhook';
    try {
      const cfg = await firstValueFrom(this.http.get<{ webhook_url?: string }>('/api/config'));
      if (cfg?.webhook_url) url = cfg.webhook_url;
    } catch {
      // Fall back to the browser origin.
    }

    try {
      await navigator.clipboard.writeText(url);
      this.errorMsg.set('');
      this.webhookHint.set('Webhook URL copied (' + url + '). Set the content type to application/json in the provider webhook settings.');
      if (this.webhookHintTimer) clearTimeout(this.webhookHintTimer);
      this.webhookHintTimer = setTimeout(() => this.webhookHint.set(''), 8000);
    } catch {
      this.errorMsg.set('Unable to copy to the clipboard. Webhook URL: ' + url);
    }
  }

  async syncNow(repo: Repository): Promise<void> {
    this.syncEvents.markSyncing(repo.id, repo.name);
    this.successMsg.set('Sync triggered for ' + repo.name + '.');
    this.errorMsg.set('');
    try {
      await firstValueFrom(this.http.post('/api/repositories/' + repo.id + '/sync', {}));
    } catch (err) {
      this.syncEvents.clearSyncing(repo.id, repo.name);
      this.errorMsg.set(getApiErrorMessage(err, 'Failed to trigger sync.'));
    }
  }

  async deleteRepo(repo: Repository): Promise<void> {
    if (!confirm('Delete repository "' + repo.name + '"? This cannot be undone.')) return;
    this.deletingId.set(repo.id);
    this.successMsg.set('');
    this.errorMsg.set('');
    try {
      await firstValueFrom(this.http.delete('/api/repositories/' + repo.id));
      this.successMsg.set('Repository "' + repo.name + '" deleted.');
      if (this.repos().length === 1 && this.page() > 1) {
        this.router.navigate([], { queryParams: { page: this.page() - 1 }, queryParamsHandling: 'merge' });
      } else {
        await this.loadRepositories(true);
      }
    } catch (err) {
      this.errorMsg.set(getApiErrorMessage(err, 'Failed to delete repository.'));
    } finally {
      this.deletingId.set(null);
    }
  }

  private async loadRepositories(showLoading = true): Promise<void> {
    if (showLoading) {
      this.loading.set(true);
    }
    try {
      const params: Record<string, string> = {
        page: String(this.page()),
        page_size: String(this.pageSize),
      };
      if (this.searchQuery) params['q'] = this.searchQuery;

      const res = await firstValueFrom(this.http.get<PagedResult<Repository>>('/api/repositories', { params }));
      this.repos.set(res?.items ?? []);
      this.total.set(res?.total ?? 0);
      this.totalPages.set(Math.max(1, Math.ceil((res?.total ?? 0) / this.pageSize)));
      this.errorMsg.set('');
    } catch (err) {
      this.repos.set([]);
      this.errorMsg.set(getApiErrorMessage(err, 'Unable to load repositories.'));
    } finally {
      if (showLoading) {
        this.loading.set(false);
      }
    }
  }
}

