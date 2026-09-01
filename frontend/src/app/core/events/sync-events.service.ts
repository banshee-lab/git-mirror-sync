import { Injectable, NgZone, inject, signal } from '@angular/core';
import { Observable, Subject } from 'rxjs';
import { SyncSseEvent } from '../models';

@Injectable({
  providedIn: 'root',
})
export class SyncEventsService {
  private readonly ngZone = inject(NgZone);
  private eventSource: EventSource | null = null;
  private readonly eventsSubject = new Subject<SyncSseEvent>();

  // Track active syncing repository IDs and names
  readonly activeSyncingRepoIds = signal<Set<number>>(new Set());
  readonly activeSyncingRepoNames = signal<Set<string>>(new Set());

  readonly events$: Observable<SyncSseEvent> = this.eventsSubject.asObservable();

  constructor() {
    this.connect();
  }

  connect(): void {
    if (typeof window === 'undefined' || typeof EventSource === 'undefined') {
      return;
    }

    if (this.eventSource && this.eventSource.readyState !== EventSource.CLOSED) {
      return;
    }

    try {
      this.eventSource = new EventSource('/api/events');

      const handleEvent = (event: MessageEvent) => {
        try {
          const data: SyncSseEvent = JSON.parse(event.data);
          this.ngZone.run(() => {
            this.processEvent(data);
            this.eventsSubject.next(data);
          });
        } catch {
          // Ignore parse errors (e.g. keepalive comments)
        }
      };

      this.eventSource.addEventListener('sync', handleEvent);
      this.eventSource.onmessage = handleEvent;

      this.eventSource.onerror = () => {
        // EventSource will automatically attempt to reconnect in the background.
      };
    } catch {
      // Ignore initial setup errors
    }
  }

  private processEvent(event: SyncSseEvent): void {
    const isStarting = event.status === 'running' || event.type === 'sync_started';
    const isFinished =
      event.status === 'success' ||
      event.status === 'error' ||
      event.status === 'conflict' ||
      event.status === 'skipped' ||
      event.type === 'sync_completed' ||
      event.type === 'sync_failed';

    if (isStarting) {
      if (event.repo_id) {
        this.activeSyncingRepoIds.update(set => {
          const next = new Set(set);
          next.add(event.repo_id!);
          return next;
        });
      }
      if (event.repo_name) {
        this.activeSyncingRepoNames.update(set => {
          const next = new Set(set);
          next.add(event.repo_name!);
          return next;
        });
      }
    } else if (isFinished) {
      if (event.repo_id) {
        this.activeSyncingRepoIds.update(set => {
          const next = new Set(set);
          next.delete(event.repo_id!);
          return next;
        });
      }
      if (event.repo_name) {
        this.activeSyncingRepoNames.update(set => {
          const next = new Set(set);
          next.delete(event.repo_name!);
          return next;
        });
      }
    }
  }

  isSyncing(repoId: number, repoName?: string): boolean {
    return (
      this.activeSyncingRepoIds().has(repoId) ||
      (repoName ? this.activeSyncingRepoNames().has(repoName) : false)
    );
  }

  hasActiveSyncs(): boolean {
    return this.activeSyncingRepoIds().size > 0 || this.activeSyncingRepoNames().size > 0;
  }

  markSyncing(repoId: number, repoName?: string): void {
    this.activeSyncingRepoIds.update(set => new Set(set).add(repoId));
    if (repoName) {
      this.activeSyncingRepoNames.update(set => new Set(set).add(repoName));
    }
  }

  clearSyncing(repoId: number, repoName?: string): void {
    this.activeSyncingRepoIds.update(set => {
      const next = new Set(set);
      next.delete(repoId);
      return next;
    });
    if (repoName) {
      this.activeSyncingRepoNames.update(set => {
        const next = new Set(set);
        next.delete(repoName);
        return next;
      });
    }
  }
}
