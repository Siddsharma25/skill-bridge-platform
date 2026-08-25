import type { ConsumeMessage } from 'amqplib';
import { RabbitmqConsumer } from './rabbitmq.consumer';
import type { RabbitmqConnectionService } from './rabbitmq-connection.service';
import type { NotificationsService } from '../notifications/notifications.service';
import type { PinoLogger } from 'nestjs-pino';

// These are the phase's required unit-test proof points: a malformed
// message is nack'd in a way that dead-letters it (channel.nack(msg,
// false, false) — no requeue, which is what makes RabbitMQ apply the
// queue's x-dead-letter-exchange argument, see topology.ts), and the
// consumer callback never throws/hangs even when something downstream
// misbehaves unexpectedly. The actual "did it really land in the DLQ"
// proof is the live verification against a real RabbitMQ broker (see
// docs/DECISIONS.md's Testing section for why broker integration tests
// don't run in CI) — this test proves the *decision* (nack vs ack, and
// which nack) is correct given a message's content, which is the part
// that's actually a bug risk to get subtly wrong.

function flushMicrotasks(): Promise<void> {
  return new Promise((resolve) => setImmediate(resolve));
}

function fakeMessage(body: unknown): ConsumeMessage {
  const content = Buffer.isBuffer(body)
    ? body
    : Buffer.from(JSON.stringify(body));
  return {
    content,
    fields: {} as ConsumeMessage['fields'],
    properties: {} as ConsumeMessage['properties'],
  };
}

function makeFakeChannel() {
  let capturedCallback: ((msg: ConsumeMessage | null) => void) | undefined;
  return {
    prefetch: jest.fn().mockResolvedValue(undefined),
    consume: jest.fn(
      (_queue: string, cb: (msg: ConsumeMessage | null) => void) => {
        capturedCallback = cb;
        return Promise.resolve({ consumerTag: 'test-consumer-tag' });
      },
    ),
    cancel: jest.fn().mockResolvedValue(undefined),
    ack: jest.fn(),
    nack: jest.fn(),
    getCallback: () => capturedCallback,
  };
}

// registerBeforeClose is called unconditionally at the end of a
// successful onModuleInit (see RabbitmqConnectionService.beforeClose's
// doc comment for why) — every fake connection below needs it, even
// though these tests don't exercise shutdown ordering themselves.
function makeFakeConnection(channel: unknown) {
  return {
    channel,
    registerBeforeClose: jest.fn(),
  } as unknown as RabbitmqConnectionService;
}

function makeSilentLogger(): PinoLogger {
  return {
    warn: jest.fn(),
    error: jest.fn(),
    info: jest.fn(),
    debug: jest.fn(),
    trace: jest.fn(),
    fatal: jest.fn(),
  } as unknown as PinoLogger;
}

const validPayload = {
  message_id: '11111111-1111-1111-1111-111111111111',
  user_id: '22222222-2222-2222-2222-222222222222',
  email: 'new-user@example.com',
  event_type: 'welcome',
  created_at: '2026-08-25T00:00:00.000Z',
};

// validPayload with one required field removed, without ever binding
// (and immediately discarding) the removed value — an unused destructured
// binding trips @typescript-eslint/no-unused-vars under this project's
// strict lint config.
function payloadMissing(
  field: keyof typeof validPayload,
): Record<string, unknown> {
  const copy: Record<string, unknown> = { ...validPayload };
  delete copy[field];
  return copy;
}

describe('RabbitmqConsumer', () => {
  it('nacks-without-requeue (routes to the DLQ) on unparseable JSON, and never calls NotificationsService', async () => {
    const channel = makeFakeChannel();
    const connection = makeFakeConnection(channel);
    const recordWelcomeEmail = jest.fn();
    const notifications = {
      recordWelcomeEmail,
    } as unknown as NotificationsService;

    const consumer = new RabbitmqConsumer(
      connection,
      notifications,
      makeSilentLogger(),
    );
    await consumer.onModuleInit();

    channel.getCallback()!(fakeMessage(Buffer.from('this is not json')));
    await flushMicrotasks();

    expect(channel.nack).toHaveBeenCalledTimes(1);
    expect(channel.nack).toHaveBeenCalledWith(expect.anything(), false, false);
    expect(channel.ack).not.toHaveBeenCalled();
    expect(recordWelcomeEmail).not.toHaveBeenCalled();
  });

  it('nacks-without-requeue on valid JSON missing a required field', async () => {
    const channel = makeFakeChannel();
    const connection = makeFakeConnection(channel);
    const recordWelcomeEmail = jest.fn();
    const notifications = {
      recordWelcomeEmail,
    } as unknown as NotificationsService;

    const consumer = new RabbitmqConsumer(
      connection,
      notifications,
      makeSilentLogger(),
    );
    await consumer.onModuleInit();

    channel.getCallback()!(fakeMessage(payloadMissing('event_type')));
    await flushMicrotasks();

    expect(channel.nack).toHaveBeenCalledWith(expect.anything(), false, false);
    expect(channel.ack).not.toHaveBeenCalled();
    expect(recordWelcomeEmail).not.toHaveBeenCalled();
  });

  it('acks a well-formed message after recording it', async () => {
    const channel = makeFakeChannel();
    const connection = makeFakeConnection(channel);
    const recordWelcomeEmail = jest.fn().mockResolvedValue('inserted');
    const notifications = {
      recordWelcomeEmail,
    } as unknown as NotificationsService;

    const consumer = new RabbitmqConsumer(
      connection,
      notifications,
      makeSilentLogger(),
    );
    await consumer.onModuleInit();

    channel.getCallback()!(fakeMessage(validPayload));
    await flushMicrotasks();

    expect(recordWelcomeEmail).toHaveBeenCalledWith(validPayload);
    expect(channel.ack).toHaveBeenCalledTimes(1);
    expect(channel.nack).not.toHaveBeenCalled();
  });

  it('does not ack or nack a null message (server-cancelled consumer) and does not throw', async () => {
    const channel = makeFakeChannel();
    const connection = makeFakeConnection(channel);
    const recordWelcomeEmail = jest.fn();
    const notifications = {
      recordWelcomeEmail,
    } as unknown as NotificationsService;

    const consumer = new RabbitmqConsumer(
      connection,
      notifications,
      makeSilentLogger(),
    );
    await consumer.onModuleInit();

    expect(() => channel.getCallback()!(null)).not.toThrow();
    await flushMicrotasks();

    expect(channel.ack).not.toHaveBeenCalled();
    expect(channel.nack).not.toHaveBeenCalled();
  });

  it('never throws out of the consume callback even when NotificationsService rejects unexpectedly', async () => {
    // This is the "consumer doesn't crash/hang" guarantee: consume()'s
    // callback isn't awaited by amqplib, so a synchronously-thrown/
    // rejected promise here would otherwise become an unhandled
    // rejection and could take the whole process down.
    const channel = makeFakeChannel();
    const connection = makeFakeConnection(channel);
    const recordWelcomeEmail = jest.fn().mockRejectedValue(new Error('boom'));
    const notifications = {
      recordWelcomeEmail,
    } as unknown as NotificationsService;

    const consumer = new RabbitmqConsumer(
      connection,
      notifications,
      makeSilentLogger(),
    );
    await consumer.onModuleInit();

    expect(() =>
      channel.getCallback()!(fakeMessage(validPayload)),
    ).not.toThrow();
    await flushMicrotasks();
    // Message is left unacked (neither ack nor nack) rather than guessed
    // at — see the consume() callback's doc comment in rabbitmq.consumer.ts.
    expect(channel.ack).not.toHaveBeenCalled();
    expect(channel.nack).not.toHaveBeenCalled();
  });
});
