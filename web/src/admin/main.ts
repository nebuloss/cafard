import './admin.css';
import { AdminApp } from './app';

const root = document.getElementById('root');
if (root) void new AdminApp(root).start();
