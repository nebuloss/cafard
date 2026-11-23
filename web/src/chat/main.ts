import './chat.css';
import { ChatApp } from './app';

const root = document.getElementById('root');
if (root) void new ChatApp(root).start();
