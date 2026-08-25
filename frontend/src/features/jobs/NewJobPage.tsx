import { useNavigate } from "react-router-dom";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQuery } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Checkbox } from "@/components/ui/checkbox";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { fetchSkills } from "@/features/skills/api";
import { createJob } from "@/features/jobs/api";
import { GraphQLError } from "@/lib/graphql-client";

const schema = z.object({
  title: z.string().min(1, "Required"),
  description: z.string().min(1, "Required"),
  requiredSkillIds: z.array(z.string()).min(1, "Pick at least one required skill"),
});

type FormValues = z.infer<typeof schema>;

export function NewJobPage() {
  const navigate = useNavigate();
  const { data: skills } = useQuery({ queryKey: ["skills"], queryFn: fetchSkills });

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { title: "", description: "", requiredSkillIds: [] },
  });

  const mutation = useMutation({
    mutationFn: ({ title, description, requiredSkillIds }: FormValues) =>
      createJob(title, description, requiredSkillIds),
    onSuccess: (job) => {
      navigate(`/jobs/${job.id}`);
    },
  });

  return (
    <div className="mx-auto max-w-2xl p-8">
      <Card>
        <CardHeader>
          <CardTitle>Post a job</CardTitle>
          <CardDescription>Required skills drive the matching worker.</CardDescription>
        </CardHeader>
        <CardContent>
          <Form {...form}>
            <form
              className="grid gap-4"
              onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
            >
              <FormField
                control={form.control}
                name="title"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Title</FormLabel>
                    <FormControl>
                      <Input {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="description"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Description</FormLabel>
                    <FormControl>
                      <Textarea rows={4} {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="requiredSkillIds"
                render={() => (
                  <FormItem>
                    <FormLabel>Required skills</FormLabel>
                    {!skills?.length && (
                      <p className="text-muted-foreground text-sm">
                        No skills exist yet — add some on the Skills page first.
                      </p>
                    )}
                    <div className="grid gap-2">
                      {skills?.map((skill) => (
                        <FormField
                          key={skill.id}
                          control={form.control}
                          name="requiredSkillIds"
                          render={({ field }) => {
                            const checked = field.value.includes(skill.id);
                            return (
                              <FormItem className="flex flex-row items-center gap-2">
                                <FormControl>
                                  <Checkbox
                                    checked={checked}
                                    onCheckedChange={(value) => {
                                      field.onChange(
                                        value
                                          ? [...field.value, skill.id]
                                          : field.value.filter((id) => id !== skill.id),
                                      );
                                    }}
                                  />
                                </FormControl>
                                <FormLabel className="font-normal">
                                  {skill.name} <span className="text-muted-foreground">({skill.category})</span>
                                </FormLabel>
                              </FormItem>
                            );
                          }}
                        />
                      ))}
                    </div>
                    <FormMessage />
                  </FormItem>
                )}
              />
              {mutation.isError && (
                <p className="text-destructive text-sm">
                  {mutation.error instanceof GraphQLError
                    ? mutation.error.message
                    : "Something went wrong. Try again."}
                </p>
              )}
              <Button type="submit" disabled={mutation.isPending}>
                {mutation.isPending ? "Posting..." : "Post job"}
              </Button>
            </form>
          </Form>
        </CardContent>
      </Card>
    </div>
  );
}
