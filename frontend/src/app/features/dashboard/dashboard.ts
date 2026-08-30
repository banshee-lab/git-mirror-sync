import { DatePipe } from '@angular/common';
import { HttpClient } from '@angular/common/http';
import { Component, OnInit, inject, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { firstValueFrom } from 'rxjs';
import { HistoryEvent, PagedResult, Repository } from '../../core/models';
import { getApiErrorMessage } from '../../core/http/api-error';
import { AlertBannerComponent } from '../../shared/alert-banner/alert-banner.component';
import { ProviderIconComponent } from '../../shared/provider-icon/provider-icon.component';
import { StatusBadgeComponent } from '../../shared/status-badge/status-badge.component';

@Component({
  selector: 'app-dashboard',
  standalone: true,
  imports: [DatePipe, RouterLink, AlertBannerComponent, ProviderIconComponent, StatusBadgeComponent],
  template: `
    <div class="space-y-6">
      <!-- Page heading -->
      <div>
        <h2 class="text-2xl font-semibold text-white">Dashboard</h2>
        <p class="text-sm text-slate-400">Overview of your mirror repositories and recent activity.</p>
      </div>

      <!-- Top-level errors -->
      <app-alert-banner [message]="reposError()" type="error" />
      <app-alert-banner [message]="eventsError()" type="error" />

      <!-- Two-column grid -->
      <div class="grid gap-6 md:grid-cols-2">

        <!-- ===== Left: Repositories ===== -->
        <div class="flex flex-col rounded-2xl border border-slate-800 bg-slate-900 overflow-hidden">
          <!-- Header -->
          <div class="flex items-center justify-between px-5 py-4 border-b border-slate-800">
            <h3 class="text-base font-semibold text-white">Repositories</h3>
            <a routerLink="/repositories"
               class="text-xs text-emerald-400 hover:text-emerald-300 hover:underline transition-colors">
              View all
            </a>
          </div>

          <!-- Body -->
          @if (reposLoading()) {
            <div class="flex-1 flex items-center justify-center py-10 text-slate-400 text-sm">
              Loading&hellip;
            </div>
          } @else if (repos().length === 0) {
            <div class="flex-1 flex flex-col items-center justify-center py-10 gap-3 text-slate-400">
              <i class="fa-solid fa-code-branch text-2xl text-slate-600"></i>
              <p class="text-sm">No repositories configured yet.</p>
              <a routerLink="/repositories/new"
                 class="text-xs text-emerald-400 hover:underline">Add one &rarr;</a>
            </div>
          } @else {
            <ul class="divide-y divide-slate-800">
              @for (repo of repos(); track repo.id) {
                <li class="flex items-center gap-3 px-5 py-3.5 hover:bg-slate-800/30 transition-colors">
                  <!-- Provider icons: pull → push -->
                  <div class="flex items-center gap-1 shrink-0">
                    <app-provider-icon [provider]="repo.provider_hint || 'generic'" size="sm" />
                    <i class="fa-solid fa-arrow-right text-slate-600 text-xs"></i>
                    <app-provider-icon [provider]="repo.push_provider_hint || 'generic'" size="sm" />
                  </div>

                  <!-- Name + last sync -->
                  <div class="min-w-0 flex-1">
                    <a [routerLink]="['/repositories', repo.id, 'edit']"
                       class="block font-medium text-sm text-white truncate hover:text-emerald-400 transition-colors">
                      {{ repo.name }}
                    </a>
                    @if (repo.last_sync_at) {
                      <span class="text-xs text-slate-500">
                        Last sync {{ repo.last_sync_at | date:'short' }}
                      </span>
                    } @else {
                      <span class="text-xs text-slate-600">Never synced</span>
                    }
                  </div>

                  <!-- Status -->
                  <div class="shrink-0">
                    <app-status-badge
                      [status]="repo.last_sync_status || (repo.enabled ? 'live' : 'disabled')" />
                  </div>
                </li>
              }
            </ul>
          }
        </div>

        <!-- ===== Right: Recent activity ===== -->
        <div class="flex flex-col rounded-2xl border border-slate-800 bg-slate-900 overflow-hidden">
          <!-- Header -->
          <div class="flex items-center justify-between px-5 py-4 border-b border-slate-800">
            <h3 class="text-base font-semibold text-white">Recent activity</h3>
            <a routerLink="/activity-log"
               class="text-xs text-emerald-400 hover:text-emerald-300 hover:underline transition-colors">
              View all
            </a>
          </div>

          <!-- Body -->
          @if (eventsLoading()) {
            <div class="flex-1 flex items-center justify-center py-10 text-slate-400 text-sm">
              Loading&hellip;
            </div>
          } @else if (events().length === 0) {
            <div class="flex-1 flex flex-col items-center justify-center py-10 gap-3 text-slate-400">
              <i class="fa-solid fa-clock-rotate-left text-2xl text-slate-600"></i>
              <p class="text-sm">No sync events yet.</p>
            </div>
          } @else {
            <ul class="divide-y divide-slate-800">
              @for (event of events(); track event.id) {
                <li class="flex items-center gap-3 px-5 py-3.5 hover:bg-slate-800/30 transition-colors">
                  <!-- Status -->
                  <div class="shrink-0">
                    <app-status-badge [status]="event.status" />
                  </div>

                  <!-- Repo name + time -->
                  <div class="min-w-0 flex-1">
                    <span class="block text-sm font-medium text-white truncate">
                      {{ event.repo_full_name || 'unknown' }}
                    </span>
                    @if (event.received_at) {
                      <span class="text-xs text-slate-500">
                        {{ event.received_at | date:'short' }}
                      </span>
                    }
                  </div>
                </li>
              }
            </ul>
          }
        </div>

      </div>
    </div>
  `,
})
export class DashboardComponent implements OnInit {
  private readonly http = inject(HttpClient);

  readonly repos = signal<Repository[]>([]);
  readonly reposLoading = signal(true);
  readonly reposError = signal('');

  readonly events = signal<HistoryEvent[]>([]);
  readonly eventsLoading = signal(true);
  readonly eventsError = signal('');

  ngOnInit(): void {
    void this.loadRepositories();
    void this.loadEvents();
  }

  private async loadRepositories(): Promise<void> {
    try {
      const res = await firstValueFrom(
        this.http.get<PagedResult<Repository>>('/api/repositories', {
          params: { page: '1', page_size: '5' },
        })
      );
      // Sort by most-recently-synced first, repos never synced go to the bottom
      const items = (res?.items ?? []).slice().sort((a, b) => {
        if (!a.last_sync_at && !b.last_sync_at) return 0;
        if (!a.last_sync_at) return 1;
        if (!b.last_sync_at) return -1;
        return new Date(b.last_sync_at).getTime() - new Date(a.last_sync_at).getTime();
      });
      this.repos.set(items);
    } catch (err) {
      this.reposError.set(getApiErrorMessage(err, 'Unable to load repositories.'));
    } finally {
      this.reposLoading.set(false);
    }
  }

  private async loadEvents(): Promise<void> {
    try {
      const events = await firstValueFrom(
        this.http.get<HistoryEvent[]>('/api/history', { params: { limit: '10' } })
      );
      this.events.set(events ?? []);
    } catch (err) {
      this.eventsError.set(getApiErrorMessage(err, 'Unable to load recent activity.'));
    } finally {
      this.eventsLoading.set(false);
    }
  }
}
