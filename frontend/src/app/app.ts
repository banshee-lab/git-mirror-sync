import { Component, HostListener, OnInit } from '@angular/core';
import { RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';
import { AuthService } from './core/auth/auth.service';

@Component({
  selector: 'app-root',
  standalone: true,
  imports: [RouterLink, RouterLinkActive, RouterOutlet],
  styleUrl: './app.css',
  templateUrl: './app.html',
})
export class App implements OnInit {
  readonly navItems = [
    { label: 'Dashboard', path: '/', icon: 'fa-solid fa-gauge', exact: true },
    { label: 'Repositories', path: '/repositories', icon: 'fa-solid fa-code-branch', exact: false },
    { label: 'SSH Keys', path: '/ssh-keys', icon: 'fa-solid fa-key', exact: false },
    { label: 'Activity Log', path: '/activity-log', icon: 'fa-solid fa-clock-rotate-left', exact: false },
  ];

  readonly mobileBreakpoint = 768;
  menuCollapsed = false;

  constructor(public readonly auth: AuthService) {}

  ngOnInit(): void {
    this.syncMenuState();
    void this.auth.refreshUser();
  }

  get isMobile(): boolean {
    return window.innerWidth < this.mobileBreakpoint;
  }

  toggleMenu(): void {
    this.menuCollapsed = !this.menuCollapsed;
  }

  @HostListener('window:resize')
  onResize(): void {
    this.syncMenuState();
  }

  private syncMenuState(): void {
    if (this.isMobile) {
      this.menuCollapsed = true;
    }
  }
}
