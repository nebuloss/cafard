import './chat.css';
import { i18n } from '@shared/i18n';
import { Theme } from '@shared/theme';
import { ChatApp } from './app';

i18n.apply();
Theme.paintBar();

const root = document.getElementById('root');
if (root) void new ChatApp(root).start();
