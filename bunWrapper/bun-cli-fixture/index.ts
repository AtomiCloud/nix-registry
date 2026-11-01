import isOdd from 'is-odd';

const arg = process.argv[2];

if (arg === '--version') {
  console.log('0.1.0');
} else {
  // Prints a value computed by the bundled dependency, so a missing or broken node_modules fails the smoke.
  console.log(`hello from bun-cli-fixture: 3 is ${isOdd(3) ? 'odd' : 'even'}`);
}
