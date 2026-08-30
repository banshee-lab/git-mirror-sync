import { Routes } from '@angular/router';
import { authGuard } from './core/auth/auth.guard';
import { ActivityLogComponent } from './features/activity-log/activity-log';
import { DashboardComponent } from './features/dashboard/dashboard';
import { LoginComponent } from './features/login/login';
import { RepositoriesComponent } from './features/repositories/repositories';
import { RepositoryFormComponent } from './features/repository-form/repository-form';
import { SshKeysComponent } from './features/ssh-keys/ssh-keys';

export const routes: Routes = [
  { path: '', component: DashboardComponent, canActivate: [authGuard] },
  { path: 'login', component: LoginComponent },
  {
    path: 'repositories',
    component: RepositoriesComponent,
    canActivate: [authGuard],
  },
  {
    path: 'repositories/new',
    component: RepositoryFormComponent,
    canActivate: [authGuard],
  },
  {
    path: 'repositories/:id/edit',
    component: RepositoryFormComponent,
    canActivate: [authGuard],
  },
  {
    path: 'ssh-keys',
    component: SshKeysComponent,
    canActivate: [authGuard],
  },
  {
    path: 'activity-log',
    component: ActivityLogComponent,
    canActivate: [authGuard],
  },
  { path: 'history', redirectTo: 'activity-log', pathMatch: 'full' },
  { path: '**', redirectTo: '' },
];

