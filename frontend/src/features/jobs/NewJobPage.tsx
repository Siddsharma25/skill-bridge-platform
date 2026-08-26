import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQuery } from "@tanstack/react-query";
import { FileClock, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { fetchSkills } from "@/features/skills/api";
import { createJob } from "@/features/jobs/api";
import { GraphQLError } from "@/lib/graphql-client";
import { clearDraft, loadDraft, saveDraft, type JobDraft } from "@/lib/storage/jobDraftDb";
import { chipColor, cn, gradientButton } from "@/lib/utils";

const schema = z.object({
  title: z.string().min(1, "Required"),
  description: z.string().min(1, "Required"),
  requiredSkillIds: z.array(z.string()).min(1, "Pick at least one required skill"),
});

type FormValues = z.infer<typeof schema>;

const emptyDraft: FormValues = { title: "", description: "", requiredSkillIds: [] };

export function NewJobPage() {
  const navigate = useNavigate();
  const { data: skills } = useQuery({ queryKey: ["skills"], queryFn: fetchSkills });
  const [restoredDraft, setRestoredDraft] = useState(false);

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: emptyDraft,
  });

  // Restore a draft (if IndexedDB has one) once, on mount — after this,
  // the watch effect below takes over as the single writer.
  useEffect(() => {
    loadDraft().then((draft) => {
      if (draft && (draft.title || draft.description || draft.requiredSkillIds.length > 0)) {
        form.reset(draft);
        setRestoredDraft(true);
      }
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps -- runs once, matching the empty deps below
  }, []);

  // Debounced autosave: react-hook-form's watch fires on every keystroke,
  // but IndexedDB writes are async and there's no need to persist every
  // single character — 400ms of no further changes is what actually
  // triggers a write.
  const saveTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => {
    const subscription = form.watch((values) => {
      clearTimeout(saveTimer.current);
      saveTimer.current = setTimeout(() => {
        const draft = values as JobDraft;
        if (draft.title || draft.description || (draft.requiredSkillIds?.length ?? 0) > 0) {
          void saveDraft(draft);
        }
      }, 400);
    });
    return () => {
      subscription.unsubscribe();
      clearTimeout(saveTimer.current);
    };
  }, [form]);

  function discardDraft() {
    void clearDraft();
    form.reset(emptyDraft);
    setRestoredDraft(false);
  }

  const mutation = useMutation({
    mutationFn: ({ title, description, requiredSkillIds }: FormValues) =>
      createJob(title, description, requiredSkillIds),
    onSuccess: (job) => {
      void clearDraft();
      navigate(`/jobs/${job.id}`);
    },
  });

  return (
    <div className="mx-auto max-w-2xl p-8">
      <Card className="animate-in-up">
        <CardHeader>
          <CardTitle>Post a job</CardTitle>
          <CardDescription>Required skills drive the matching worker.</CardDescription>
        </CardHeader>
        <CardContent>
          {restoredDraft && (
            <div className="bg-secondary/60 mb-4 flex items-center justify-between gap-2 rounded-md px-3 py-2 text-sm">
              <span className="flex items-center gap-1.5">
                <FileClock className="size-3.5" /> Restored a draft saved in this browser.
              </span>
              <button
                type="button"
                onClick={discardDraft}
                className="text-muted-foreground hover:text-foreground flex items-center gap-1"
              >
                <X className="size-3.5" /> Discard
              </button>
            </div>
          )}
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
                    <div className="flex flex-wrap gap-2">
                      {skills?.map((skill) => (
                        <FormField
                          key={skill.id}
                          control={form.control}
                          name="requiredSkillIds"
                          render={({ field }) => {
                            const checked = field.value.includes(skill.id);
                            return (
                              <button
                                type="button"
                                onClick={() =>
                                  field.onChange(
                                    checked
                                      ? field.value.filter((id) => id !== skill.id)
                                      : [...field.value, skill.id],
                                  )
                                }
                                className={cn(
                                  "rounded-full border px-3 py-1 text-sm font-medium transition-all active:scale-95",
                                  checked
                                    ? "border-0 bg-gradient-to-r from-fuchsia-500 via-violet-500 to-sky-500 text-white shadow-md shadow-violet-500/30"
                                    : cn("border-transparent", chipColor(skill.id)),
                                )}
                              >
                                {skill.name}
                                <span className={cn("ml-1", checked ? "text-white/70" : "opacity-70")}>
                                  {skill.category}
                                </span>
                              </button>
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
              <Button type="submit" disabled={mutation.isPending} className={gradientButton}>
                {mutation.isPending ? "Posting..." : "Post job"}
              </Button>
            </form>
          </Form>
        </CardContent>
      </Card>
    </div>
  );
}
