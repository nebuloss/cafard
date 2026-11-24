import './admin.css';
import { i18n } from '@shared/i18n';
import { AdminApp } from './app';

i18n.apply();
const root = document.getElementById('root');
if (root) void new AdminApp(root).start();
