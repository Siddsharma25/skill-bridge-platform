import { Module } from '@nestjs/common';
import { DbModule } from '../db/db.module';
import { RabbitmqModule } from '../rabbitmq/rabbitmq.module';
import { HealthController } from './health.controller';

@Module({
  imports: [DbModule, RabbitmqModule],
  controllers: [HealthController],
})
export class HealthModule {}
