import { DatePipe } from '@angular/common';
import { HttpClient } from '@angular/common/http';
import { Component, OnInit, inject, signal } from '@angular/core';
import { firstValueFrom } from 'rxjs';
import { HistoryEvent } from '../../core/models';
import { getApiErrorMessage } from '../../core/http/api-error';
import { StatusBadgeComponent } from '../../shared/status-badge/status-badge.component';
import { AlertBannerComponent } from '../../shared/alert-banner/alert-banner.component';

@Component({
  selector: 'app-activity-log',
  standalone: true,
  imports: [DatePipe, StatusBadgeComponent, AlertBannerComponent],
  template: `
    <div class="space-y-6">
      <div>
        <h2 class="text-2xl font-semibold text-white">Activity Log</h2>
        <p class="text-sm text-slate-400">Recent successful, conflicted, and failed mirror attempts.</p>
      </div>

      @if (loading()) {
        <div class="rounded-2xl border border-slate-800 bg-slate-900 p-6 text-slate-300">Loading activity log…</div>
      } @else if (error()) {
        <app-alert-banner [message]="error()" type="error" />
      } @else {
        <div class="overflow-hidden rounded-2xl border border-slate-800 bg-slate-900">
          <table class="min-w-full text-left text-sm text-slate-300">
            <thead class="bg-slate-950/60 text-xs uppercase tracking-wide text-slate-400">
              <tr>
                <th class="px-4 py-3">Time</th>
                <th class="px-4 py-3">Repo</th>
                <th class="px-4 py-3">Status</th>
                <th class="px-4 py-3">Detail</th>
              </tr>
            </thead>
            <tbody>
              @for (event of events(); track event.id) {
                <tr class="border-t border-slate-800 hover:bg-slate-800/30 transition-colors">
                  <td class="px-4 py-3 text-slate-300 whitespace-nowrap">
                    {{ event.received_at ? (event.received_at | date: 'yyyy-MM-dd HH:mm:ss') : '—' }}
                  </td>
                  <td class="px-4 py-3 font-medium text-white">{{ event.repo_full_name || 'unknown' }}</td>
                  <td class="px-4 py-3">
                    <app-status-badge [status]="event.status" />
                  </td>
                  <td class="px-4 py-3 text-slate-300">{{ event.detail || 'No additional detail.' }}</td>
                </tr>
              } @empty {
                <tr>
                  <td colspan="4" class="px-4 py-6 text-center text-slate-400">No activity events yet.</td>
                </tr>
              }
            </tbody>
          </table>
        </div>
      }
    </div>
  `,
})
export class ActivityLogComponent implements OnInit {
  private readonly http = inject(HttpClient);

  readonly events = signal<HistoryEvent[]>([]);
  readonly loading = signal(true);
  readonly error = signal('');

  ngOnInit(): void {
    void this.loadHistory();
  }

  async loadHistory(): Promise<void> {
    try {
      const events = await firstValueFrom(this.http.get<HistoryEvent[]>('/api/history'));
      this.events.set(events ?? []);
      this.error.set('');
    } catch (err) {
      this.events.set([]);
      this.error.set(getApiErrorMessage(err, 'Unable to load activity log.'));
    } finally {
      this.loading.set(false);
    }
  }
}
