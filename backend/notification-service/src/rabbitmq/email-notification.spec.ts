import {
  MalformedMessageError,
  parseEmailNotification,
} from './email-notification';

describe('parseEmailNotification', () => {
  const valid = {
    message_id: 'm-1',
    user_id: 'u-1',
    email: 'a@example.com',
    event_type: 'welcome',
    created_at: '2026-08-25T00:00:00.000Z',
  };

  it('parses a well-formed payload', () => {
    const result = parseEmailNotification(Buffer.from(JSON.stringify(valid)));
    expect(result).toEqual(valid);
  });

  it('throws MalformedMessageError on invalid JSON', () => {
    expect(() => parseEmailNotification(Buffer.from('{not json'))).toThrow(
      MalformedMessageError,
    );
  });

  it('throws MalformedMessageError on a JSON array', () => {
    expect(() => parseEmailNotification(Buffer.from('[1,2,3]'))).toThrow(
      MalformedMessageError,
    );
  });

  it('throws MalformedMessageError on JSON null', () => {
    expect(() => parseEmailNotification(Buffer.from('null'))).toThrow(
      MalformedMessageError,
    );
  });

  it.each(['message_id', 'user_id', 'email', 'event_type', 'created_at'])(
    'throws MalformedMessageError when %s is missing',
    (field) => {
      const payload = { ...valid } as Record<string, unknown>;
      delete payload[field];
      expect(() =>
        parseEmailNotification(Buffer.from(JSON.stringify(payload))),
      ).toThrow(MalformedMessageError);
    },
  );

  it('throws MalformedMessageError when a required field is blank', () => {
    const payload = { ...valid, email: '   ' };
    expect(() =>
      parseEmailNotification(Buffer.from(JSON.stringify(payload))),
    ).toThrow(MalformedMessageError);
  });
});
