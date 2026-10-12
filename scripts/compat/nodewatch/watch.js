const fs = require('fs');
fs.watch('/w', { recursive: true }, (ev, f) => console.log('EVENT', ev, f));
console.log('watching');
