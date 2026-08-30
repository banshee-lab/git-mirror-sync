import { Component, Input, computed } from '@angular/core';

@Component({
  selector: 'app-provider-icon',
  standalone: true,
  template: `
    <span
      [title]="title()"
      class="inline-flex items-center justify-center rounded-lg p-1.5 transition-colors"
      [class]="containerClass()"
    >
      <i [class]="iconClass()"></i>
    </span>
  `,
})
export class ProviderIconComponent {
  @Input() provider?: string;
  @Input() size: 'sm' | 'md' | 'lg' = 'md';

  readonly normalizedProvider = computed(() => {
    const p = (this.provider || '').toLowerCase().trim();
    if (p.includes('github')) return 'github';
    if (p.includes('gitlab')) return 'gitlab';
    if (p.includes('forgejo')) return 'forgejo';
    if (p.includes('gitea')) return 'gitea';
    return 'generic';
  });

  readonly title = computed(() => {
    const p = this.normalizedProvider();
    switch (p) {
      case 'github': return 'GitHub';
      case 'gitlab': return 'GitLab';
      case 'forgejo': return 'Forgejo';
      case 'gitea': return 'Gitea';
      default: return 'Git Repository';
    }
  });

  readonly iconClass = computed(() => {
    const p = this.normalizedProvider();
    const sizeMap = {
      sm: 'text-xs',
      md: 'text-sm',
      lg: 'text-base',
    };
    const sz = sizeMap[this.size] || 'text-sm';

    switch (p) {
      case 'github':
        return `fa-brands fa-github ${sz} text-white`;
      case 'gitlab':
        return `fa-brands fa-gitlab ${sz} text-orange-400`;
      case 'forgejo':
        return `fa-solid fa-code-fork ${sz} text-amber-400`;
      case 'gitea':
        return `fa-solid fa-mug-hot ${sz} text-teal-400`;
      default:
        return `fa-solid fa-code-branch ${sz} text-slate-400`;
    }
  });

  readonly containerClass = computed(() => {
    const p = this.normalizedProvider();
    switch (p) {
      case 'github':
        return 'bg-slate-800/80 border border-slate-700/60';
      case 'gitlab':
        return 'bg-orange-500/10 border border-orange-500/20';
      case 'forgejo':
        return 'bg-amber-500/10 border border-amber-500/20';
      case 'gitea':
        return 'bg-teal-500/10 border border-teal-500/20';
      default:
        return 'bg-slate-800/60 border border-slate-700/50';
    }
  });
}
