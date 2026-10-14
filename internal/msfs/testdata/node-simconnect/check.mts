import { SimConnectConnection } from '../../../../../node-simconnect/src/SimConnectConnection.ts';

const c: any = new SimConnectConnection('ATM Tracker', 5);
const packets: Buffer[] = [];
c._clientSocket.write = (b: Uint8Array) => { packets.push(Buffer.from(b)); };

c._open();
clearTimeout(c._openTimeout);
c.requestSystemState(2, 'Sim');
c.addToDataDefinition(1, 'PLANE LATITUDE', 'degrees', 4, 0, 0xffffffff);
c.requestDataOnSimObject(1, 1, 0, 4);
c.subscribeToSystemEvent(2, 'Sim');
c.subscribeToSystemEvent(1, 'Pause_EX1');
c.mapClientEventToSimEvent(3, 'PAUSE_ON');
c.transmitClientEvent(0, 3, 0, 1, 0x10);

const names = ['open', 'systemState', 'addToDataDefinition', 'requestData', 'subscribeSim', 'subscribePause', 'mapClientEvent', 'transmitClientEvent'];
for (const [i, p] of packets.entries()) {
  console.log(`${names[i]} ${p.toString('hex')}`);
}
