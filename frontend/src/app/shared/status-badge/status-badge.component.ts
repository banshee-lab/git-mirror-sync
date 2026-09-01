import { Component, Input, computed } from '@angular/core';

@Component({
  selector: 'app-status-badge',
  standalone: true,
  template: `
    <span
      class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium"
      [class]="badgeClass()"
    >
      <span class="h-1.5 w-1.5 rounded-full" [class]="dotClass()"></span>
      <span class="uppercase tracking-wider">{{ label() }}</span>
    </span>
  `,
})
export class StatusBadgeComponent {
  @Input() status: string | boolean | undefined = '';

  readonly normalizedStatus = computed(() => {
    if (typeof this.status === 'boolean') {
      return this.status ? 'live' : 'disabled';
    }
    return (this.status || '').toLowerCase().trim();
  });

  readonly label = computed(() => {
    const s = this.normalizedStatus();
    switch (s) {
      case 'live':
      case 'enabled':
      case 'true':
        return 'Live';
      case 'disabled':
      case 'false':
        return 'Disabled';
      case 'running':
      case 'syncing':
        return 'Running';
      case 'success':
        return 'Success';
      case 'conflict':
        return 'Conflict';
      case 'received':
        return 'Received';
      case 'error':
        return 'Error';
      case 'skipped':
        return 'Skipped';
      default:
        return s || 'Unknown';
    }
  });

  readonly badgeClass = computed(() => {
    const s = this.normalizedStatus();
    switch (s) {
      case 'live':
      case 'enabled':
      case 'true':
      case 'success':
        return 'bg-emerald-500/15 text-emerald-300 border border-emerald-500/20';
      case 'running':
      case 'syncing':
        return 'bg-sky-500/15 text-sky-300 border border-sky-500/30';
      case 'conflict':
      case 'received':
        return 'bg-amber-500/15 text-amber-300 border border-amber-500/20';
      case 'error':
        return 'bg-rose-500/15 text-rose-300 border border-rose-500/20';
      case 'skipped':
      case 'disabled':
      case 'false':
      default:
        return 'bg-slate-700/50 text-slate-300 border border-slate-600/30';
    }
  });

  readonly dotClass = computed(() => {
    const s = this.normalizedStatus();
    switch (s) {
      case 'live':
      case 'enabled':
      case 'true':
      case 'success':
        return 'bg-emerald-400';
      case 'running':
      case 'syncing':
        return 'bg-sky-400 animate-pulse';
      case 'conflict':
      case 'received':
        return 'bg-amber-400';
      case 'error':
        return 'bg-rose-400';
      case 'skipped':
      case 'disabled':
      case 'false':
      default:
        return 'bg-slate-400';
    }
  });
}
