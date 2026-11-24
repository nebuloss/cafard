import './chat.css';
import { i18n } from '@shared/i18n';
import { ChatApp } from './app';

i18n.apply();

const root = document.getElementById('root');
if (root) void new ChatApp(root).start();
