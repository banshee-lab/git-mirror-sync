import { Component, Input, Output, EventEmitter, computed } from '@angular/core';
@Component({
  selector: 'app-alert-banner',
  standalone: true,
  template: `
    @if (message) {
      <div
        class="flex items-center gap-3 rounded-xl border p-4 text-sm transition-all"
        [class]="containerClass()"
        role="alert"
      >
        <i [class]="iconClass()" class="shrink-0 text-base"></i>
        <div class="flex-1 break-words font-medium">
          {{ message }}
        </div>
        @if (dismissible) {
          <button
            type="button"
            (click)="dismiss.emit()"
            class="shrink-0 rounded-lg p-1 opacity-70 transition hover:opacity-100 focus:outline-none"
            aria-label="Dismiss alert"
          >
            <i class="fa-solid fa-xmark text-sm"></i>
          </button>
        }
      </div>
    }
  `,
})
export class AlertBannerComponent {
  @Input() message = '';
  @Input() type: 'error' | 'success' | 'info' | 'warning' = 'error';
  @Input() dismissible = false;
  @Output() dismiss = new EventEmitter<void>();

  readonly containerClass = computed(() => {
    switch (this.type) {
      case 'success':
        return 'border-emerald-500/30 bg-emerald-500/10 text-emerald-200';
      case 'warning':
        return 'border-amber-500/30 bg-amber-500/10 text-amber-200';
      case 'info':
        return 'border-sky-500/30 bg-sky-500/10 text-sky-200';
      case 'error':
      default:
        return 'border-rose-500/30 bg-rose-500/10 text-rose-200';
    }
  });

  readonly iconClass = computed(() => {
    switch (this.type) {
      case 'success':
        return 'fa-solid fa-circle-check text-emerald-400';
      case 'warning':
        return 'fa-solid fa-triangle-exclamation text-amber-400';
      case 'info':
        return 'fa-solid fa-circle-info text-sky-400';
      case 'error':
      default:
        return 'fa-solid fa-circle-exclamation text-rose-400';
    }
  });
}
